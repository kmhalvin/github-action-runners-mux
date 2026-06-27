package api

// Strongly typed RunnerName used across multiplexer, orchestrator, and config
type RunnerName string

// AllocateRequest is the payload sent from the shim to the orchestrator to request capacity
type AllocateRequest struct {
	RunnerName RunnerName `json:"runner_name"`
}

// AllocateResponse is the orchestrator's response back to the shim
type AllocateResponse struct {
	WorkerIP string `json:"worker_ip"`
}

// WaitResponse is the response from the worker-shim's /wait endpoint
type WaitResponse struct {
	ExitCode int `json:"exit_code"`
}
