package dashboard

import (
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "strings"
        "time"

        "github.com/kmhalvin/github-action-runners-mux/pkg/github"
)

// CookieNameForHost returns the cookie name used to store the OAuth token for a
// given host. Dots are replaced with underscores since cookie names can't
// contain dots in some browsers.
func CookieNameForHost(host string) string {
        return fmt.Sprintf("gh_token_%s", strings.ReplaceAll(host, ".", "_"))
}

// listAuthHosts returns the list of configured OAuth App hosts with their
// client IDs. The frontend uses this to build OAuth authorize URLs.
func (api *API) listAuthHosts(w http.ResponseWriter, r *http.Request) {
        if api.authCfg == nil || len(api.authCfg.OAuthApps) == 0 {
                WriteJSON(w, http.StatusOK, []any{})
                return
        }

        type hostInfo struct {
                Host     string `json:"host"`
                ClientID string `json:"client_id"`
        }

        var hosts []hostInfo
        for _, app := range api.authCfg.OAuthApps {
                hosts = append(hosts, hostInfo{
                        Host:     app.Host,
                        ClientID: app.ClientID,
                })
        }

        WriteJSON(w, http.StatusOK, hosts)
}

// exchangeToken exchanges an OAuth code for an access token. The frontend
// calls this after the OAuth redirect. The client_secret is used server-side
// only and never exposed to the frontend.
func (api *API) exchangeToken(w http.ResponseWriter, r *http.Request) {
        var payload struct {
                Code string `json:"code"`
                Host string `json:"host"`
        }

        if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
                WriteError(w, http.StatusBadRequest, "invalid payload")
                return
        }

        if payload.Code == "" || payload.Host == "" {
                WriteError(w, http.StatusBadRequest, "code and host are required")
                return
        }

        appCfg, ok := api.authCfg.GetAppConfig(payload.Host)
        if !ok {
                WriteError(w, http.StatusNotFound, fmt.Sprintf("no OAuth App configured for host %s", payload.Host))
                return
        }

        // Exchange code for access token
        tokenURL := fmt.Sprintf("https://%s/login/oauth/access_token", payload.Host)
        body := fmt.Sprintf("client_id=%s&client_secret=%s&code=%s",
                appCfg.ClientID, appCfg.ClientSecret, payload.Code)

        req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, tokenURL, strings.NewReader(body))
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
        req.Header.Set("Accept", "application/json")

        client := &http.Client{Timeout: 10 * time.Second}
        resp, err := client.Do(req)
        if err != nil {
                WriteError(w, http.StatusBadGateway, fmt.Sprintf("failed to exchange code: %v", err))
                return
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusOK {
                respBody, _ := io.ReadAll(resp.Body)
                WriteError(w, http.StatusBadGateway, fmt.Sprintf("OAuth token exchange failed: %d %s", resp.StatusCode, string(respBody)))
                return
        }

        var tokenResp struct {
                AccessToken string `json:"access_token"`
                TokenType   string `json:"token_type"`
                Scope       string `json:"scope"`
                Error       string `json:"error"`
        }
        if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
                WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to decode token response: %v", err))
                return
        }

        if tokenResp.Error != "" {
                WriteError(w, http.StatusBadGateway, fmt.Sprintf("OAuth error: %s", tokenResp.Error))
                return
        }

        if tokenResp.AccessToken == "" {
                WriteError(w, http.StatusBadGateway, "OAuth token exchange returned empty token")
                return
        }

        // Set the per-host cookie so subsequent requests are authenticated.
        // The cookie is HttpOnly so JavaScript can't read the token (XSS protection),
        // and lasts 7 days (OAuth App tokens don't expire, but this forces re-auth
        // periodically).
        cookieName := CookieNameForHost(payload.Host)
        http.SetCookie(w, &http.Cookie{
                Name:     cookieName,
                Value:    tokenResp.AccessToken,
                Path:     "/",
                MaxAge:   60 * 60 * 24 * 7, // 7 days
                HttpOnly: true,
                SameSite: http.SameSiteLaxMode,
        })

        WriteJSON(w, http.StatusOK, map[string]string{
                "access_token": tokenResp.AccessToken,
                "host":         payload.Host,
        })
}

// authStatus returns which hosts the user is currently logged into, based on
// which gh_token_* cookies exist. Also returns is_admin per host (true if the
// user's GitHub username matches the hardcoded admin check).
func (api *API) authStatus(w http.ResponseWriter, r *http.Request) {
        if api.authCfg == nil || len(api.authCfg.OAuthApps) == 0 {
                WriteJSON(w, http.StatusOK, []any{})
                return
        }

        type hostStatus struct {
                Host     string `json:"host"`
                LoggedIn bool   `json:"logged_in"`
                IsAdmin  bool   `json:"is_admin"`
        }

        var statuses []hostStatus
        for _, app := range api.authCfg.OAuthApps {
                cookieName := CookieNameForHost(app.Host)
                cookie, err := r.Cookie(cookieName)
                loggedIn := err == nil && cookie.Value != ""
                isAdmin := false
                if loggedIn {
                        if username, err := api.getOrCreateUsername(r, app.Host, cookie.Value); err == nil {
                                isAdmin = api.authCfg.IsAdmin(username, app.Host)
                        }
                }
                statuses = append(statuses, hostStatus{
                        Host:     app.Host,
                        LoggedIn: loggedIn,
                        IsAdmin:  isAdmin,
                })
        }

        WriteJSON(w, http.StatusOK, statuses)
}

// listGitHubRepos lists all orgs and repos where the user has admin access
// across all signed-in hosts. Reads the per-host cookies server-side.
func (api *API) listGitHubRepos(w http.ResponseWriter, r *http.Request) {
        if api.authCfg == nil {
                WriteJSON(w, http.StatusOK, []github.AdminRepo{})
                return
        }

        var allRepos []github.AdminRepo

        for _, app := range api.authCfg.OAuthApps {
                cookieName := CookieNameForHost(app.Host)
                cookie, err := r.Cookie(cookieName)
                if err != nil || cookie.Value == "" {
                        continue // User not signed in to this host
                }

                repos, err := github.ListAdminRepos(r.Context(), app.Host, cookie.Value)
                if err != nil {
                        // Skip this host on error (token might be expired)
                        continue
                }
                allRepos = append(allRepos, repos...)
        }

        WriteJSON(w, http.StatusOK, allRepos)
}

// logout clears the gh_token_* cookie for a specific host (via ?host= query
// param), signing the user out of that host only. If no host is provided,
// all host cookies are cleared.
func (api *API) logout(w http.ResponseWriter, r *http.Request) {
        if api.authCfg == nil || len(api.authCfg.OAuthApps) == 0 {
                WriteJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
                return
        }

        host := r.URL.Query().Get("host")

        if host != "" {
                cookieName := CookieNameForHost(host)
                http.SetCookie(w, &http.Cookie{
                        Name:   cookieName,
                        Value:  "",
                        Path:   "/",
                        MaxAge: -1,
                })
        } else {
                for _, app := range api.authCfg.OAuthApps {
                        cookieName := CookieNameForHost(app.Host)
                        http.SetCookie(w, &http.Cookie{
                                Name:   cookieName,
                                Value:  "",
                                Path:   "/",
                                MaxAge: -1,
                        })
                }
        }

        WriteJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// getOAuthTokenForHost reads the OAuth token from the cookie for the given host.
// Returns empty string and error if not signed in.
func (api *API) getOAuthTokenForHost(r *http.Request, host string) (string, error) {
        cookieName := CookieNameForHost(host)
        cookie, err := r.Cookie(cookieName)
        if err != nil || cookie.Value == "" {
                return "", fmt.Errorf("not signed in to %s", host)
        }
        return cookie.Value, nil
}
