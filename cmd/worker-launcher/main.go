package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// workerHeader mirrors the framed preamble sent by the worker-shim over the TCP
// connection. It carries the runner's config files (base64-encoded) so the
// worker-launcher can write them to /actions-runner/ before spawning
// Runner.Worker.
type workerHeader struct {
	ConfigFiles map[string]string `json:"config_files"`
}

// readFramedHeader reads a 4-byte big-endian length prefix followed by JSON
// from the connection. This must be called before the raw pipe bridge begins so
// the header bytes are consumed before any pipe data.
func readFramedHeader(conn net.Conn) (workerHeader, error) {
	var header workerHeader

	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return header, fmt.Errorf("read header length: %w", err)
	}
	payloadLen := binary.BigEndian.Uint32(lenBuf[:])

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return header, fmt.Errorf("read header payload: %w", err)
	}

	if err := json.Unmarshal(payload, &header); err != nil {
		return header, fmt.Errorf("unmarshal header: %w", err)
	}
	return header, nil
}

// writeConfigFiles writes the base64-decoded config files into the runner
// directory (/actions-runner). These files (.runner, .credentials, etc.) are
// required by ConfigurationStore.GetSettings() — without them Runner.Worker
// crashes with ArgumentNullException: 'configuredSettings'.
func writeConfigFiles(configFiles map[string]string) error {
	runnerDir := "/actions-runner"
	for fname, b64data := range configFiles {
		data, err := base64.StdEncoding.DecodeString(b64data)
		if err != nil {
			return fmt.Errorf("decode %s: %w", fname, err)
		}
		dest := filepath.Join(runnerDir, fname)
		if err := os.WriteFile(dest, data, 0600); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		log.Printf("[Standalone Mode] Wrote config file: %s (%d bytes)", dest, len(data))
	}
	return nil
}

func (wl *WorkerLauncher) runStandaloneWorker(conn net.Conn) {
	// 1. Read the framed header (config files) before starting the pipe bridge.
	// The shim sends this as the first bytes on the TCP connection.
	header, err := readFramedHeader(conn)
	if err != nil {
		log.Fatalf("Failed to read config files header: %v", err)
	}

	// 2. Write the config files to /actions-runner/ so Runner.Worker can find
	// .runner / .credentials when it calls ConfigurationStore.GetSettings().
	if len(header.ConfigFiles) > 0 {
		if err := writeConfigFiles(header.ConfigFiles); err != nil {
			log.Fatalf("Failed to write config files: %v", err)
		}
	} else {
		log.Printf("[Standalone Mode] Warning: no config files received in header")
	}

	// 3. Create local pipes
	workerRead, shimWrite, err := os.Pipe()
	if err != nil {
		log.Fatalf("Pipe creation failed: %v", err)
	}
	shimRead, workerWrite, err := os.Pipe()
	if err != nil {
		log.Fatalf("Pipe creation failed: %v", err)
	}

	// 4. Stream TCP bidirectionally to/from local pipes.
	// We track these with a WaitGroup so we can ensure all pipe data is flushed
	// to the shim before reporting the exit code.
	var wg sync.WaitGroup
	wg.Go(func() {
		io.Copy(shimWrite, conn)
		shimWrite.Close()
	})
	wg.Go(func() {
		io.Copy(conn, shimRead)
		conn.Close()
	})

	// 5. Spawn the real Runner.Worker from /actions-runner/.
	// The config files written in step 2 are now in /actions-runner/, so
	// ConfigurationStore.GetSettings() will find .runner / .credentials.
	realWorkerPath := "/actions-runner/bin/Runner.Worker"
	cmd := exec.Command(realWorkerPath, "spawnclient", "3", "4")
	cmd.Dir = "/actions-runner"
	cmd.ExtraFiles = []*os.File{workerRead, workerWrite}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[Standalone Mode] Spawning %s (cwd: %s)...", realWorkerPath, cmd.Dir)
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	err = cmd.Wait()

	// Determine the exit code.
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
		}
	}

	// The child has exited. Close its write-end of the pipe so that
	// io.Copy(conn, shimRead) gets EOF and finishes flushing any remaining
	// data to the shim. Without this, io.Copy blocks forever (the child is
	// gone but the write end is still open), causing a deadlock: the shim
	// can't fetch the exit code, the container times out after 5s, and the
	// shim receives the wrong exit code (1 instead of the real one).
	workerWrite.Close()

	// Wait for both io.Copy goroutines to finish so all pipe data is fully
	// flushed to the shim over TCP before we report the exit code.
	wg.Wait()

	wl.finish(exitCode)
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
