package orchestrator

import (
	"strings"

	"github.com/google/uuid"

	"github.com/docker/docker/api/types"
	"github.com/kmhalvin/github-action-runners-mux/api"
)

func shortID() string {
	return uuid.NewString()[:8]
}

func parseRunnerFromActiveName(name string) api.RunnerName {
	s := strings.TrimPrefix(name, namePrefixActive)
	lastDash := strings.LastIndex(s, "-")
	if lastDash > 0 {
		return api.RunnerName(s[:lastDash])
	}
	return api.RunnerName(s)
}

func firstIP(c types.Container) string {
	for _, netObj := range c.NetworkSettings.Networks {
		return netObj.IPAddress
	}
	return ""
}
