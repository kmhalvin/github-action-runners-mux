package github

import (
        "context"
        "fmt"
        "strings"

        "github.com/google/go-github/v89/github"
        "golang.org/x/oauth2"
)

// newClient creates a go-github client for a GHES host using an OAuth token.
func newClient(host, token string) *github.Client {
        ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
        tc := oauth2.NewClient(context.Background(), ts)
        baseURL := fmt.Sprintf("https://%s/api/v3", host)
        uploadURL := fmt.Sprintf("https://%s/api/uploads", host)
        client, err := github.NewClient(
                github.WithHTTPClient(tc),
                github.WithEnterpriseURLs(baseURL, uploadURL),
        )
        if err != nil {
                panic(fmt.Sprintf("failed to create github client for %s: %v", host, err))
        }
        return client
}

// HostFromURL extracts the host from a GitHub URL.
// e.g., "https://github.enterprise.local/org/repo" → "github.enterprise.local"
func HostFromURL(rawURL string) string {
        rawURL = strings.TrimSpace(rawURL)
        rawURL = strings.TrimPrefix(rawURL, "https://")
        rawURL = strings.TrimPrefix(rawURL, "http://")
        if idx := strings.Index(rawURL, "/"); idx > 0 {
                return rawURL[:idx]
        }
        return rawURL
}

// RepoInfo holds parsed repository/organization information from a GitHub URL.
type RepoInfo struct {
        Host  string
        Owner string
        Repo  string // empty for org-level URLs
}

// ParseRepoURL parses a GitHub URL into its components.
// e.g., "https://host/org/repo" → {Host, Owner: "org", Repo: "repo"}
// e.g., "https://host/org" → {Host, Owner: "org", Repo: ""}
func ParseRepoURL(rawURL string) (*RepoInfo, error) {
        rawURL = strings.TrimRight(strings.TrimSpace(rawURL), "/")
        host := HostFromURL(rawURL)

        path := strings.TrimPrefix(rawURL, "https://")
        path = strings.TrimPrefix(path, "http://")
        path = strings.TrimPrefix(path, host)
        path = strings.Trim(path, "/")

        parts := strings.Split(path, "/")
        if len(parts) < 1 || parts[0] == "" {
                return nil, fmt.Errorf("invalid URL (expected host/owner or host/owner/repo): %s", rawURL)
        }

        info := &RepoInfo{
                Host:  host,
                Owner: parts[0],
        }
        if len(parts) >= 2 {
                info.Repo = parts[1]
        }
        return info, nil
}

// AdminRepo represents a repository or organization where the user has admin access.
type AdminRepo struct {
        Host  string `json:"host"`
        Owner string `json:"owner"`
        Repo  string `json:"repo"` // empty for org-level
        URL   string `json:"url"`
        Type  string `json:"type"` // "organization" or "repository"
}

// ListAdminRepos lists all organizations and repositories where the user has
// admin access on the given host, using the user's OAuth token. Used to
// populate the repo/org picker combobox in the AddRunner form.
func ListAdminRepos(ctx context.Context, host, oauthToken string) ([]AdminRepo, error) {
        client := newClient(host, oauthToken)
        var results []AdminRepo

        // List organizations where the user has admin (owner) role.
        // Custom org roles with runner-management permission are not detectable
        // via the API without owner access, so only orgs where the user is an
        // owner are listed. Users with custom roles can still type the org URL
        // manually in the AddRunner form.
        memberships, _, err := client.Organizations.ListOrgMemberships(ctx, &github.ListOrgMembershipsOptions{
                State:       "active",
                ListOptions: github.ListOptions{PerPage: 100},
        })
        if err != nil {
                return nil, fmt.Errorf("failed to list org memberships: %w", err)
        }
        for _, m := range memberships {
                if m.GetRole() != "admin" {
                        continue
                }
                org := m.GetOrganization()
                results = append(results, AdminRepo{
                        Host:  host,
                        Owner: org.GetLogin(),
                        URL:   fmt.Sprintf("https://%s/%s", host, org.GetLogin()),
                        Type:  "organization",
                })
        }

        // List repos the user has admin access to (includes org repos).
        // Uses the non-deprecated ListByAuthenticatedUserIter iterator which
        // handles pagination internally via iter.Seq2.
        for repo, err := range client.Repositories.ListByAuthenticatedUserIter(ctx, &github.RepositoryListByAuthenticatedUserOptions{
                ListOptions: github.ListOptions{PerPage: 100},
        }) {
                if err != nil {
                        return nil, fmt.Errorf("failed to list repos: %w", err)
                }
                if repo.GetPermissions() != nil && repo.GetPermissions().GetAdmin() {
                        results = append(results, AdminRepo{
                                Host:  host,
                                Owner: repo.Owner.GetLogin(),
                                Repo:  repo.GetName(),
                                URL:   repo.GetHTMLURL(),
                                Type:  "repository",
                        })
                }
        }

        return results, nil
}

// GetRegistrationToken generates a registration token for a repo or org using
// the user's OAuth token. For repo-level: POST /repos/{owner}/{repo}/actions/runners/registration-token
// For org-level: POST /orgs/{org}/actions/runners/registration-token
// The token expires after 1 hour.
func GetRegistrationToken(ctx context.Context, host, owner, repo, oauthToken string) (string, error) {
        client := newClient(host, oauthToken)

        if repo == "" {
                // Org-level
                token, _, err := client.Actions.CreateOrganizationRegistrationToken(ctx, owner)
                if err != nil {
                        return "", fmt.Errorf("failed to create org registration token: %w", err)
                }
                return token.GetToken(), nil
        }

        // Repo-level
        token, _, err := client.Actions.CreateRegistrationToken(ctx, owner, repo)
        if err != nil {
                return "", fmt.Errorf("failed to create registration token: %w", err)
        }
        return token.GetToken(), nil
}

// GetAuthenticatedUser calls the GitHub API to get the authenticated user's
// login (username) using their OAuth token.
func GetAuthenticatedUser(ctx context.Context, host, oauthToken string) (string, error) {
        client := newClient(host, oauthToken)
        user, _, err := client.Users.Get(ctx, "")
        if err != nil {
                return "", fmt.Errorf("failed to get authenticated user: %w", err)
        }
        return user.GetLogin(), nil
}

// CheckAdminAccess checks if the given user has admin access to the specified
// repo or org. For repo-level (repo != ""): checks repository permissions.
// For org-level (repo == ""): checks org membership role.
func CheckAdminAccess(ctx context.Context, host, owner, repo, username, oauthToken string) (bool, error) {
        client := newClient(host, oauthToken)

        if repo != "" {
                // Repo-level: check repository permissions
                repository, _, err := client.Repositories.Get(ctx, owner, repo)
                if err != nil {
                        return false, fmt.Errorf("failed to get repository: %w", err)
                }
                if repository.GetPermissions() != nil {
                        return repository.GetPermissions().GetAdmin(), nil
                }
                return false, nil
        }

        // Org-level: check org membership role
        membership, _, err := client.Organizations.GetOrgMembership(ctx, username, owner)
        if err != nil {
                return false, fmt.Errorf("failed to get org membership: %w", err)
        }
        return membership.GetRole() == "admin", nil
}
