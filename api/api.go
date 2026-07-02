package api

const SockPath = "/tmp/proxy.sock"

// Strongly typed RunnerName used across multiplexer, orchestrator, and config
type RunnerName string

// AllocateRequest is the payload sent from the Worker Shim to the orchestrator to request capacity
type AllocateRequest struct {
	RunnerName RunnerName `json:"runner_name"`
}

// AllocateResponse is the orchestrator's response back to the Worker Shim
type AllocateResponse struct {
	WorkerIP string `json:"worker_ip"`
}

// WaitResponse is the response from the worker-launcher's /wait endpoint
type WaitResponse struct {
	ExitCode int `json:"exit_code"`
}

// StartRequest is the payload sent from the Orchestrator to the worker-launcher's /start endpoint
type StartRequest struct {
	JITConfig string `json:"jitConfig"`
}
