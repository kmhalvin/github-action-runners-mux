package monitor

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"sync"
)

const SocketPath = "/tmp/multiplexer.sock"

type LockMessage struct {
	Action string `json:"action"` // Should be "LOCK"
	PID    int    `json:"pid"`
	PGID   int    `json:"pgid"`
}

type IPCMonitor struct {
	activeWorker int
	activePGID   int
	workerMutex  sync.Mutex
	workerSem    chan struct{}
	onLock       func(pgid int)
	onUnlock     func(pgid int)
}

func NewIPCMonitor(onLock, onUnlock func(pgid int)) (*IPCMonitor, error) {
	return &IPCMonitor{
		workerSem: make(chan struct{}, 1),
		onLock:    onLock,
		onUnlock:  onUnlock,
	}, nil
}

func (m *IPCMonitor) GetActivePGID() int {
	m.workerMutex.Lock()
	defer m.workerMutex.Unlock()
	return m.activePGID
}

func (m *IPCMonitor) Start() {
	// Clean up old socket
	_ = os.Remove(SocketPath)

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: SocketPath, Net: "unix"})
	if err != nil {
		log.Fatalf("[Mutex] Failed to start IPC socket: %v", err)
	}
	// Allow shim to connect
	if err := os.Chmod(SocketPath, 0777); err != nil {
		log.Printf("[Mutex] Warning: Could not chmod socket: %v", err)
	}

	log.Printf("[Mutex] Listening for Shim locks on %s", SocketPath)

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			log.Printf("[Mutex] Accept error: %v", err)
			continue
		}

		go m.handleConnection(conn)
	}
}

func (m *IPCMonitor) handleConnection(conn *net.UnixConn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("[Mutex] Failed to read from shim: %v", err)
		return
	}

	var msg LockMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		log.Printf("[Mutex] Invalid message format: %v", err)
		return
	}

	if msg.Action != "LOCK" {
		log.Printf("[Mutex] Unknown action: %s", msg.Action)
		return
	}

	// Wait in queue (blocks if another worker is active)
	log.Printf("[Mutex] Shim (PID: %d, PGID: %d) waiting in queue for available slot...", msg.PID, msg.PGID)
	m.workerSem <- struct{}{}
	defer func() { <-m.workerSem }()

	log.Printf("[Mutex] Shim (PID: %d, PGID: %d) acquired slot. Engaging lock...", msg.PID, msg.PGID)

	m.workerMutex.Lock()
	m.activeWorker = msg.PID
	m.activePGID = msg.PGID
	m.onLock(msg.PGID)
	m.workerMutex.Unlock()

	// Send ACK to shim so it can execute syscall.Exec
	_, err = conn.Write([]byte("ACK\n"))
	if err != nil {
		log.Printf("[Mutex] Failed to send ACK to shim: %v", err)
		// We should probably unlock here if the shim crashed before Exec
		m.releaseLock(msg.PID, msg.PGID)
		return
	}

	// Block until EOF (The worker process inheriting the FD exits)
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == io.EOF {
		log.Printf("[Mutex] Received EOF from socket (Worker exited). Releasing lock...")
	} else if err != nil {
		log.Printf("[Mutex] Socket closed with error: %v. Releasing lock...", err)
	} else {
		log.Printf("[Mutex] Socket received unexpected data. Releasing lock...")
	}

	m.releaseLock(msg.PID, msg.PGID)
}

func (m *IPCMonitor) releaseLock(pid, pgid int) {
	m.workerMutex.Lock()
	defer m.workerMutex.Unlock()

	if m.activeWorker == pid {
		m.activeWorker = 0
		m.activePGID = 0
		m.onUnlock(pgid)
	}
}
