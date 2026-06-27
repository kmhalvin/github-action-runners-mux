package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

const sockPath = "/tmp/multiplexer.sock"

type AllocateResponse struct {
	WorkerIP string `json:"worker_ip"`
}

func main() {
	if len(os.Args) < 4 {
		log.Fatalf("[Shim] Expected at least 4 arguments, got %d", len(os.Args))
	}

	fdOutStr := os.Args[2]
	fdInStr := os.Args[3]

	fdOut, err := strconv.Atoi(fdOutStr)
	if err != nil {
		log.Fatalf("[Shim] Invalid fdOut: %v", err)
	}
	fdIn, err := strconv.Atoi(fdInStr)
	if err != nil {
		log.Fatalf("[Shim] Invalid fdIn: %v", err)
	}

	// Read Controller URL from environment, default to localhost for local testing
	execPath, _ := os.Executable()
	runnerName := filepath.Base(filepath.Dir(filepath.Dir(execPath)))
	
	reqBody := fmt.Sprintf(`{"runner_name": "%s"}`, runnerName)

	log.Printf("[Shim:%s] Requesting ephemeral worker from orchestrator...", runnerName)

	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Post("http://unix/api/v1/worker/allocate", "application/json", bytes.NewBuffer([]byte(reqBody)))
	if err != nil {
		log.Fatalf("[Shim] Failed to allocate worker: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("[Shim] Controller rejected allocation: %s", string(body))
	}

	var allocResponse AllocateResponse
	if err := json.NewDecoder(resp.Body).Decode(&allocResponse); err != nil {
		log.Fatalf("[Shim] Failed to decode allocation response: %v", err)
	}

	workerIP := allocResponse.WorkerIP
	log.Printf("[Shim] Worker allocated at IP: %s", workerIP)

	// 2. Connect to Worker TCP Stream
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:9000", workerIP))
	if err != nil {
		log.Fatalf("[Shim] Failed to connect to worker pipe stream: %v", err)
	}
	defer conn.Close()

	// 3. Map File Descriptors
	// pipeHandleOut is where Runner.Listener writes, so it's a Read fd for the Shim.
	listenerWriteWorkerReadFile := os.NewFile(uintptr(fdOut), "pipeHandleOut")
	// pipeHandleIn is where Runner.Listener reads, so it's a Write fd for the Shim.
	listenerReadWorkerWriteFile := os.NewFile(uintptr(fdIn), "pipeHandleIn")

	errChan := make(chan error, 2)

	// Stream 1: Listener -> TCP (Worker Read)
	go func() {
		_, err := io.Copy(conn, listenerWriteWorkerReadFile)
		errChan <- err
	}()

	// Stream 2: TCP (Worker Write) -> Listener
	go func() {
		_, err := io.Copy(listenerReadWorkerWriteFile, conn)
		errChan <- err
	}()

	// Wait for streams to close (usually when worker exits)
	<-errChan

	// 4. Get Exit Code from Worker HTTP
	log.Printf("[Shim] Streams closed. Fetching exit code from worker...")
	exitResp, err := http.Get(fmt.Sprintf("http://%s:9001/wait", workerIP))
	if err != nil {
		log.Fatalf("[Shim] Failed to get exit code: %v", err)
	}
	defer exitResp.Body.Close()

	var exitData map[string]int
	if err := json.NewDecoder(exitResp.Body).Decode(&exitData); err != nil {
		log.Fatalf("[Shim] Failed to decode exit code: %v", err)
	}

	exitCode := exitData["exit_code"]
	log.Printf("[Shim] Remote worker finished with exit code %d. Exiting...", exitCode)
	os.Exit(exitCode)
}
