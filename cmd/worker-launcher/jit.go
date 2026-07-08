package main

import (
	"log"
	"os"
	"os/exec"
)

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
