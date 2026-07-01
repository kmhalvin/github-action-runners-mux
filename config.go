package main

import (
	"fmt"
	"os"

	"github.com/kmhalvin/github-action-runners-mux/pkg/api"

	"gopkg.in/yaml.v3"
)

type RunnerConfig struct {
	Name         api.RunnerName `yaml:"name"`
	URL          string         `yaml:"url"`
	PAT          string         `yaml:"pat"`
	ScaleSetName string         `yaml:"scale_set_name"`
	Labels       []string       `yaml:"labels,omitempty"`
	Group        string         `yaml:"group,omitempty"`
	MaxRunners   int            `yaml:"max_runners,omitempty"`
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

	for _, r := range cfg.Runners {
		if r.Name == "" || r.URL == "" || r.PAT == "" || r.ScaleSetName == "" {
			return nil, fmt.Errorf("runner configuration is missing required fields (name, url, pat, scale_set_name)")
		}
	}

	return &cfg, nil
}
