package config

// RunnerConfig is the in-memory configuration for a runner. It is constructed
// from the database (sqlc.Runner) and passed to the standalone/scaleset
// managers. It is never serialized to or from YAML.
type RunnerConfig struct {
	Name         string
	Mode         string // "standalone" or "scaleset"
	URL          string
	Token        string // For standalone (registration token, auto-generated via OAuth)
	Dir          string // For standalone
	PAT          string // For scaleset
	ScaleSetName string // For scaleset
	MaxRunners   int    // Override global max_workers
	// Labels are only used for standalone mode; ignored for scaleset mode
	Labels []string
	Group  string
}

// MuxMeta holds metadata for a runner directory. The registration token is no
// longer stored here because it expires after 1 hour. Deregistration requires
// a fresh token provided by the user at deletion time.
type MuxMeta struct {
	RunnerName string `json:"runner_name,omitempty"`
	URL        string `json:"url"`
}
