package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/api"
)

type WorkerLauncher struct {
	exitCode    int
	finished    bool
	started     bool
	mutex       sync.Mutex
	cond        *sync.Cond
	waitFetched chan struct{}
	startOnce   sync.Once
}

func NewWorkerLauncher() *WorkerLauncher {
	wl := &WorkerLauncher{
		waitFetched: make(chan struct{}, 1),
	}
	wl.cond = sync.NewCond(&wl.mutex)
	return wl
}

func (wl *WorkerLauncher) handleWait(w http.ResponseWriter, r *http.Request) {
	wl.mutex.Lock()
	for !wl.finished {
		wl.cond.Wait()
	}
	exitCode := wl.exitCode
	wl.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(api.WaitResponse{ExitCode: exitCode})

	// Signal that the host has fetched the response
	select {
	case wl.waitFetched <- struct{}{}:
	default:
	}
}

func (wl *WorkerLauncher) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req api.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	startedHere := false
	wl.startOnce.Do(func() {
		startedHere = true
		w.WriteHeader(http.StatusOK)
		go wl.runJITWorker(req.JITConfig)
	})

	if !startedHere {
		http.Error(w, "Worker already started in another mode", http.StatusConflict)
	}
}

func (wl *WorkerLauncher) runJITWorker(jitConfig string) {
	cmd := exec.Command("/actions-runner/run.sh")
	cmd.Env = append(os.Environ(), "ACTIONS_RUNNER_INPUT_JITCONFIG="+jitConfig)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[JIT Mode] Spawning run.sh with JIT config...")
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start listener with JIT config: %v", err)
		wl.finish(1)
		return
	}

	err := cmd.Wait()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			wl.finish(exitError.ExitCode())
		} else {
			wl.finish(1)
		}
	} else {
		wl.finish(0)
	}
}

func (wl *WorkerLauncher) runStandaloneWorker(conn net.Conn) {
	// Create local pipes
	workerRead, shimWrite, err := os.Pipe()
	if err != nil {
		log.Fatalf("Pipe creation failed: %v", err)
	}
	shimRead, workerWrite, err := os.Pipe()
	if err != nil {
		log.Fatalf("Pipe creation failed: %v", err)
	}

	// Stream TCP bidirectionally to/from local pipes
	go func() {
		io.Copy(shimWrite, conn)
		shimWrite.Close()
	}()
	go func() {
		io.Copy(conn, shimRead)
		conn.Close()
	}()

	realWorkerPath := "/actions-runner/bin/Runner.Worker"
	cmd := exec.Command(realWorkerPath, "spawnclient", "3", "4")
	cmd.ExtraFiles = []*os.File{workerRead, workerWrite}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[Standalone Mode] Spawning %s...", realWorkerPath)
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	err = cmd.Wait()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			wl.finish(exitError.ExitCode())
		} else {
			wl.finish(1)
		}
	} else {
		wl.finish(0)
	}
}

func (wl *WorkerLauncher) finish(code int) {
	wl.mutex.Lock()
	wl.exitCode = code
	wl.finished = true
	wl.cond.Broadcast()
	wl.mutex.Unlock()
	log.Printf("Worker finished with exit code: %d", code)
}

func main() {
	wl := NewWorkerLauncher()

	// HTTP Server
	go func() {
		http.HandleFunc("/wait", wl.handleWait)
		http.HandleFunc("/start", wl.handleStart)
		log.Println("Worker Launcher HTTP server listening on :9001")
		if err := http.ListenAndServe(":9001", nil); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// TCP Server
	go func() {
		listener, err := net.Listen("tcp", "0.0.0.0:9000")
		if err != nil {
			log.Fatalf("TCP listen failed: %v", err)
		}
		log.Println("Worker Launcher TCP server listening on :9000")

		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("TCP accept failed: %v", err)
				continue
			}

			startedHere := false
			wl.startOnce.Do(func() {
				startedHere = true
				log.Println("TCP connection established. Entering Standalone Mode.")
				go wl.runStandaloneWorker(conn)
			})

			if !startedHere {
				log.Println("Rejecting TCP connection: Worker already started in another mode.")
				conn.Close()
			} else {
				// Only accept one connection for standalone mode
				break
			}
		}
	}()

	// Wait for the worker to finish
	wl.mutex.Lock()
	for !wl.finished {
		wl.cond.Wait()
	}
	exitCode := wl.exitCode
	wl.mutex.Unlock()

	log.Printf("Worker payload finished. Exiting container in 5 seconds if not scraped...")

	// Robust shutdown: wait for the host to fetch the exit code, with a 5s fallback
	// JIT mode doesn't fetch /wait, so this timeout gracefully exits the container for JIT.
	select {
	case <-wl.waitFetched:
		log.Println("Exit code successfully delivered to host.")
	case <-time.After(5 * time.Second):
		log.Println("Timeout waiting for host to fetch exit code (or JIT mode completed). Terminating.")
	}

	os.Exit(exitCode)
}
