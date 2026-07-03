package api

const SockPath = "/tmp/proxy.sock"

// Strongly typed RunnerName used across multiplexer, orchestrator, and config
type RunnerName string

// AllocateRequest is the payload sent from the Worker Shim to the orchestrator to request capacity.
// RunnerDir is the absolute path to the runner's working directory (where .runner/.credentials live).
// The shim derives this from its own executable path, making it authoritative — the orchestrator
// uses it to read the runner's config files without relying on a name match.
type AllocateRequest struct {
	RunnerName RunnerName `json:"runner_name"`
	RunnerDir  string     `json:"runner_dir,omitempty"`
}

// AllocateResponse is the orchestrator's response back to the Worker Shim.
// ConfigFiles carries the base64-encoded contents of the runner's config files
// (.runner, .credentials, .credentials_rsaparams, .agent) so the worker
// container can run Runner.Worker without mounting the shared volume (which
// would expose all runners' credentials — a security violation).
type AllocateResponse struct {
	WorkerIP    string            `json:"worker_ip"`
	ConfigFiles map[string]string `json:"config_files,omitempty"`
}

// WaitResponse is the response from the worker-launcher's /wait endpoint
type WaitResponse struct {
	ExitCode int `json:"exit_code"`
}

// StartRequest is the payload sent from the Orchestrator to the worker-launcher's /start endpoint
type StartRequest struct {
	JITConfig string `json:"jitConfig"`
}
