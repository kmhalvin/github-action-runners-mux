package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type RunnerConfig struct {
	Name         string         `yaml:"name"`
	Mode         string         `yaml:"mode"` // "standalone" or "scaleset"
	URL          string         `yaml:"url"`
	Token        string         `yaml:"token,omitempty"`          // For standalone
	Dir          string         `yaml:"dir,omitempty"`            // For standalone
	PAT          string         `yaml:"pat,omitempty"`            // For scaleset
	ScaleSetName string         `yaml:"scale_set_name,omitempty"` // For scaleset
	MaxRunners   int            `yaml:"max_runners,omitempty"`    // Override global max_workers
	Labels       []string       `yaml:"labels,omitempty"`
	Group        string         `yaml:"group,omitempty"`
}

type Config struct {
	MaxWorkers  int            `yaml:"max_workers,omitempty"`
	WarmWorkers int            `yaml:"warm_workers,omitempty"`
	Runners     []RunnerConfig `yaml:"runners"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if len(cfg.Runners) == 0 {
		return nil, fmt.Errorf("no runners configured")
	}

	seenNames := make(map[string]bool)
	seenDirs := make(map[string]bool)

	for i := range cfg.Runners {
		if cfg.Runners[i].Mode == "" {
			cfg.Runners[i].Mode = "standalone" // default
		}
		r := cfg.Runners[i]

		if r.Name == "" || r.URL == "" {
			return nil, fmt.Errorf("runner configuration is missing required fields (name, url)")
		}

		if seenNames[r.Name] {
			return nil, fmt.Errorf("duplicate runner name detected: %s", r.Name)
		}
		seenNames[r.Name] = true

		switch r.Mode {
		case "standalone":
			if r.Dir == "" {
				return nil, fmt.Errorf("standalone runner [%s] is missing required field: dir", r.Name)
			}
			if seenDirs[r.Dir] {
				return nil, fmt.Errorf("duplicate standalone runner directory detected: %s (used by %s)", r.Dir, r.Name)
			}
			seenDirs[r.Dir] = true
		case "scaleset":
			if r.PAT == "" || r.ScaleSetName == "" {
				return nil, fmt.Errorf("scaleset runner [%s] is missing required fields: pat, scale_set_name", r.Name)
			}
		default:
			return nil, fmt.Errorf("runner [%s] has invalid mode '%s' (must be 'standalone' or 'scaleset')", r.Name, r.Mode)
		}
	}

	return &cfg, nil
}
