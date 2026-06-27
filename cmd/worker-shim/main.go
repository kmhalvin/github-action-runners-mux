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
)

type WorkerShim struct {
	exitCode int
	finished bool
	mutex    sync.Mutex
	cond     *sync.Cond
}

func NewWorkerShim() *WorkerShim {
	ws := &WorkerShim{}
	ws.cond = sync.NewCond(&ws.mutex)
	return ws
}

func (ws *WorkerShim) handleWait(w http.ResponseWriter, r *http.Request) {
	ws.mutex.Lock()
	for !ws.finished {
		ws.cond.Wait()
	}
	exitCode := ws.exitCode
	ws.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"exit_code": exitCode})
}

func main() {
	shim := NewWorkerShim()

	// HTTP Server for /wait
	go func() {
		http.HandleFunc("/wait", shim.handleWait)
		log.Println("Worker Shim HTTP server listening on :9001")
		if err := http.ListenAndServe(":9001", nil); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// TCP Server for Pipe Proxy
	listener, err := net.Listen("tcp", "0.0.0.0:9000")
	if err != nil {
		log.Fatalf("TCP listen failed: %v", err)
	}
	log.Println("Worker Shim TCP server listening on :9000")

	// Accept a single connection
	conn, err := listener.Accept()
	if err != nil {
		log.Fatalf("TCP accept failed: %v", err)
	}
	defer conn.Close()

	log.Println("TCP connection established with Listener Shim.")

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

	// Spawn Runner.Worker directly from the baked-in image directory
	realWorkerPath := "/actions-runner/bin/Runner.Worker"
	
	cmd := exec.Command(realWorkerPath, "spawnclient", "3", "4")
	// FD 3 will be workerRead, FD 4 will be workerWrite
	cmd.ExtraFiles = []*os.File{workerRead, workerWrite}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Spawning %s...", realWorkerPath)
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	// Wait for worker to finish
	err = cmd.Wait()
	
	shim.mutex.Lock()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			shim.exitCode = exitError.ExitCode()
		} else {
			shim.exitCode = 1
		}
	} else {
		shim.exitCode = 0
	}
	shim.finished = true
	shim.cond.Broadcast()
	shim.mutex.Unlock()

	log.Printf("Worker finished with exit code: %d", shim.exitCode)
	
	// Keep process alive for a few seconds so Listener Shim can fetch the exit code
	// Usually the HTTP /wait handler holds the connection, so once it returns, it's done.
}
