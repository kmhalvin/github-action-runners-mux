package orchestrator

import (
	"strings"

	"github.com/google/uuid"

	"github.com/docker/docker/api/types"
)

func shortID() string {
	return uuid.NewString()[:8]
}

func parseRunnerFromActiveName(name string) string {
	s := strings.TrimPrefix(name, namePrefixActive)
	lastDash := strings.LastIndex(s, "-")
	if lastDash > 0 {
		return s[:lastDash]
	}
	return s
}

func firstIP(c types.Container) string {
	for _, netObj := range c.NetworkSettings.Networks {
		return netObj.IPAddress
	}
	return ""
}
