package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/api"
	"github.com/kmhalvin/github-action-runners-mux/config"
)

// workerHeader is the framed preamble sent over the TCP connection before the
// raw pipe bridge begins. It carries the runner's config files (base64-encoded)
// so the worker-launcher can write them to /actions-runner/ before spawning
// Runner.Worker. This avoids mounting the shared runner-data volume (which would
// expose all runners' credentials to the CI job).
type workerHeader struct {
	ConfigFiles map[string]string `json:"config_files"`
}

func main() {
	if len(os.Args) < 4 {
		log.Fatalf("[Worker Shim] Expected at least 4 arguments, got %d", len(os.Args))
	}

	fdOutStr := os.Args[2]
	fdInStr := os.Args[3]

	fdOut, err := strconv.Atoi(fdOutStr)
	if err != nil {
		log.Fatalf("[Worker Shim] Invalid fdOut: %v", err)
	}
	fdIn, err := strconv.Atoi(fdInStr)
	if err != nil {
		log.Fatalf("[Worker Shim] Invalid fdIn: %v", err)
	}

	// Derive the runner's working directory from the shim's own executable path.
	// The shim lives at <cfg.Dir>/bin/Runner.Worker, so:
	//   filepath.Dir(execPath)            = <cfg.Dir>/bin
	//   filepath.Dir(filepath.Dir(execPath)) = <cfg.Dir>          (e.g. /opt/runners/backend)
	// This is authoritative — it's where config.sh wrote .runner/.credentials.
	execPath, _ := os.Executable()
	runnerDir := filepath.Dir(filepath.Dir(execPath))
	runnerName := filepath.Base(runnerDir)
	var meta config.MuxMeta
	if nameBytes, err := os.ReadFile(filepath.Join(runnerDir, ".mux-meta.json")); err == nil {
		if err := json.Unmarshal(nameBytes, &meta); err == nil && meta.RunnerName != "" {
			runnerName = meta.RunnerName
		}
	}
	reqBody, _ := json.Marshal(api.AllocateRequest{
		RunnerName: runnerName,
		RunnerDir:  runnerDir,
	})

	log.Printf("[Worker Shim:%s] Requesting ephemeral worker from orchestrator...", runnerName)

	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", api.SockPath)
			},
		},
	}

	resp, err := client.Post("http://unix/api/v1/worker/allocate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Fatalf("[Worker Shim] Failed to allocate worker: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("[Worker Shim] Orchestrator rejected allocation: %s", string(body))
	}

	var allocResponse api.AllocateResponse
	if err := json.NewDecoder(resp.Body).Decode(&allocResponse); err != nil {
		log.Fatalf("[Worker Shim] Failed to decode allocation response: %v", err)
	}

	workerIP := allocResponse.WorkerIP
	configFiles := readRunnerConfigFiles(runnerDir)
	log.Printf("[Worker Shim] Worker allocated at IP: %s (config files: %d)", workerIP, len(configFiles))

	// 2. Connect to Worker TCP Stream
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:9000", workerIP))
	if err != nil {
		log.Fatalf("[Worker Shim] Failed to connect to worker pipe stream: %v", err)
	}
	defer conn.Close()

	// 2a. Send framed header so the worker-launcher receives the runner's config
	// files. The header is a 4-byte big-endian length prefix followed by JSON.
	// After the header, the connection becomes a raw bidirectional byte pipe.
	if err := writeFramedHeader(conn, workerHeader{ConfigFiles: configFiles}); err != nil {
		log.Fatalf("[Worker Shim] Failed to send config files header: %v", err)
	}

	// 3. Map File Descriptors
	// pipeHandleOut is where Runner.Listener writes, so it's a Read fd for the Worker Shim.
	listenerWriteWorkerReadFile := os.NewFile(uintptr(fdOut), "pipeHandleOut")
	// pipeHandleIn is where Runner.Listener reads, so it's a Write fd for the Worker Shim.
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

	// Wait for ONE stream to close. We must NOT wait for both — Stream 1
	// (listener -> TCP) blocks on the listener's pipe, which only closes when
	// this shim process exits. Waiting for both would deadlock.
	//
	// The worker-launcher's fix (close workerWrite + wg.Wait) ensures all
	// worker data is flushed to TCP before the connection closes. When the
	// connection closes, Stream 2 finishes, we fetch the exit code, and exit.
	// Stream 1 is killed by os.Exit, which is fine because the worker has
	// already exited (no more data needs to flow from listener to worker).
	<-errChan

	// 4. Get Exit Code from Worker HTTP
	// Retry with backoff: the worker-launcher may be in a brief race between
	// flushing the HTTP response and os.Exit(), or the 5s container exit
	// timeout may fire before our first attempt lands. Retries handle both.
	log.Printf("[Worker Shim] Streams closed. Fetching exit code from worker...")

	var exitData api.WaitResponse
	var lastErr error
	waitClient := &http.Client{Timeout: 3 * time.Second}
	waitURL := fmt.Sprintf("http://%s:9001/wait", workerIP)

	for attempt := range 5 {
		exitResp, err := waitClient.Get(waitURL)

		if err != nil {
			lastErr = err
			log.Printf("[Worker Shim] /wait attempt %d failed: %v", attempt+1, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err := json.NewDecoder(exitResp.Body).Decode(&exitData); err != nil {
			exitResp.Body.Close()
			lastErr = err
			log.Printf("[Worker Shim] /wait attempt %d decode failed: %v", attempt+1, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		exitResp.Body.Close()
		lastErr = nil
		break
	}
	if lastErr != nil {
		log.Fatalf("[Worker Shim] Failed to get exit code after retries: %v", lastErr)
	}

	exitCode := exitData.ExitCode
	log.Printf("[Worker Shim] Remote worker finished with exit code %d. Exiting...", exitCode)
	os.Exit(exitCode)
}

// writeFramedHeader writes a 4-byte big-endian length prefix followed by the
// JSON-encoded header to the connection.
func writeFramedHeader(conn net.Conn, header workerHeader) error {
	payload, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("marshal header: %w", err)
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))

	if _, err := conn.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}
