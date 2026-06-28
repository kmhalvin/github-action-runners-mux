package api

// Strongly typed RunnerName used across multiplexer, orchestrator, and config
type RunnerName string

// StartRequest is the payload sent from the Orchestrator to the worker-launcher's /start endpoint
type StartRequest struct {
	JITConfig string `json:"jitConfig"`
}
