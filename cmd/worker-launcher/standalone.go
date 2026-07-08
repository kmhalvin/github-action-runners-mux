package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

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
		dest := filepath.Join(runnerDir, filepath.Base(fname))
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
		log.Printf("Failed to read config files header: %v", err)
		wl.finish(1)
		return
	}

	// 2. Write the config files to /actions-runner/ so Runner.Worker can find
	// .runner / .credentials when it calls ConfigurationStore.GetSettings().
	if len(header.ConfigFiles) > 0 {
		if err := writeConfigFiles(header.ConfigFiles); err != nil {
			log.Printf("Failed to write config files: %v", err)
			wl.finish(1)
			return
		}
	} else {
		log.Printf("[Standalone Mode] Warning: no config files received in header")
	}

	// 3. Create local pipes
	workerRead, shimWrite, err := os.Pipe()
	if err != nil {
		log.Printf("Pipe creation failed: %v", err)
		wl.finish(1)
		return
	}
	shimRead, workerWrite, err := os.Pipe()
	if err != nil {
		log.Printf("Pipe creation failed: %v", err)
		wl.finish(1)
		return
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
		log.Printf("Failed to start worker: %v", err)
		wl.finish(1)
		return
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
