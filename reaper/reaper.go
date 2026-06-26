package reaper

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// StartZombieReaper runs in the background and reaps dead child processes.
// Because the Go proxy acts as PID 1 in the container, it must wait() on orphaned children.
func StartZombieReaper() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGCHLD)

	go func() {
		for {
			<-c
			reapZombies()
		}
	}()
}

func reapZombies() {
	for {
		var wstatus syscall.WaitStatus
		// WNOHANG ensures we don't block if there are no dead children
		pid, err := syscall.Wait4(-1, &wstatus, syscall.WNOHANG, nil)
		if err != nil || pid <= 0 {
			break
		}
		log.Printf("[Reaper] Reaped zombie process (PID: %d, exit status: %d)", pid, wstatus.ExitStatus())
	}
}
