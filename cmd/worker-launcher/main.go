package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
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
