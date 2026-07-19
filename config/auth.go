package config

import (
        "fmt"
        "os"

        "gopkg.in/yaml.v3"
)

// OAuthAppConfig holds the credentials for an OAuth App registered on a GHES
// host. The OAuth App is used for user authentication — users sign in via the
// OAuth flow, and the resulting token (with repo scope) is used to list repos
// and generate registration tokens.
type OAuthAppConfig struct {
        Host         string `yaml:"host"`
        ClientID     string `yaml:"client_id"`
        ClientSecret string `yaml:"client_secret"`
}

// AdminEntry represents a user with admin privileges on a specific host.
type AdminEntry struct {
	Host     string `yaml:"host"`
	Username string `yaml:"username"`
}

// AuthConfig is the top-level structure for auth.yaml.
type AuthConfig struct {
	OAuthApps []OAuthAppConfig `yaml:"oauth_apps"`
	Admins    []AdminEntry     `yaml:"admins"`
}

// LoadAuthConfig reads the auth config from the given path. If the file does
// not exist, an empty AuthConfig is returned (auth is optional — without it,
// the dashboard is open with no login gate).
func LoadAuthConfig(path string) (*AuthConfig, error) {
        data, err := os.ReadFile(path)
        if err != nil {
                if os.IsNotExist(err) {
                        return &AuthConfig{}, nil
                }
                return nil, fmt.Errorf("failed to read auth config: %w", err)
        }

        var cfg AuthConfig
        if err := yaml.Unmarshal(data, &cfg); err != nil {
                return nil, fmt.Errorf("failed to parse auth config: %w", err)
        }

        return &cfg, nil
}

// GetAppConfig returns the OAuth App config for the given host.
func (a *AuthConfig) GetAppConfig(host string) (*OAuthAppConfig, bool) {
        for i := range a.OAuthApps {
                if a.OAuthApps[i].Host == host {
                        return &a.OAuthApps[i], true
                }
        }
        return nil, false
}

// Hosts returns a list of all configured host names.
func (a *AuthConfig) Hosts() []string {
	hosts := make([]string, len(a.OAuthApps))
	for i, app := range a.OAuthApps {
		hosts[i] = app.Host
	}
	return hosts
}

// IsAdmin checks if the given username is an admin on the specified host.
func (a *AuthConfig) IsAdmin(username, host string) bool {
	for _, admin := range a.Admins {
		if admin.Host == host && admin.Username == username {
			return true
		}
	}
	return false
}
