package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

const SocketPath = "/tmp/multiplexer.sock"

type LockMessage struct {
	Action string `json:"action"`
	PID    int    `json:"pid"`
	PGID   int    `json:"pgid"`
}

func main() {
	// 1. Connect to the Proxy IPC Server
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: SocketPath, Net: "unix"})
	if err != nil {
		log.Fatalf("[Shim] Failed to connect to proxy IPC: %v", err)
	}
	// Note: We deliberately DO NOT defer conn.Close() here, because we want it to survive syscall.Exec

	// 2. Clear the O_CLOEXEC flag on the socket so the worker inherits it
	sysConn, err := conn.SyscallConn()
	if err != nil {
		log.Fatalf("[Shim] Failed to get syscall conn: %v", err)
	}

	err = sysConn.Control(func(fd uintptr) {
		// Get current flags
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
		if errno != 0 {
			log.Fatalf("[Shim] Failed to get fd flags: %v", errno)
		}
		// Clear FD_CLOEXEC flag (usually 1)
		flags &^= syscall.FD_CLOEXEC
		// Set flags back
		_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, flags)
		if errno != 0 {
			log.Fatalf("[Shim] Failed to set fd flags: %v", errno)
		}
	})
	if err != nil {
		log.Fatalf("[Shim] Syscall control error: %v", err)
	}

	pid := os.Getpid()
	pgid, _ := syscall.Getpgid(pid)

	// 3. Send LOCK request
	msg := LockMessage{
		Action: "LOCK",
		PID:    pid,
		PGID:   pgid,
	}
	payload, _ := json.Marshal(msg)
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		log.Fatalf("[Shim] Failed to send lock message: %v", err)
	}

	// 4. Wait for ACK from proxy
	reader := bufio.NewReader(conn)
	ack, err := reader.ReadString('\n')
	if err != nil || ack != "ACK\n" {
		log.Fatalf("[Shim] Failed to receive ACK from proxy: %v", err)
	}

	// 5. Handover execution to Runner.Worker.real
	binDir := filepath.Dir(os.Args[0])
	realWorkerPath := filepath.Join(binDir, "Runner.Worker.real")

	// Adjust os.Args[0] to point to the real worker
	args := os.Args
	args[0] = realWorkerPath

	// syscall.Exec replaces the current process image.
	// The inherited open socket remains open in the new process.
	err = syscall.Exec(realWorkerPath, args, os.Environ())
	if err != nil {
		log.Fatalf("[Shim] syscall.Exec failed: %v", err)
	}
}
