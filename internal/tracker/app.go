package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AppCredentials holds the credentials returned after GitHub App creation.
type AppCredentials struct {
	AppID         int    `json:"id"`
	AppSlug       string `json:"slug"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	HTMLURL       string `json:"html_url"`
}

// appManifest builds the GitHub App manifest JSON.
func appManifest(webhookURL, redirectURL string) map[string]interface{} {
	return map[string]interface{}{
		"name":        "maestro-bridge",
		"url":         "https://github.com/weslien/maestro",
		"description": "Maestro webhook bridge for GitHub Projects V2 events",
		"public":      true,
		"hook_attributes": map[string]interface{}{
			"url":    webhookURL,
			"active": true,
		},
		"redirect_url": redirectURL,
		"default_permissions": map[string]string{
			"organization_projects": "read",
		},
		"default_events": []string{
			"projects_v2_item",
		},
	}
}

// CreateApp runs the GitHub App manifest flow:
// 1. Starts a local callback server
// 2. Opens the browser to GitHub's app creation page with the manifest
// 3. Waits for the redirect with a temporary code
// 4. Exchanges the code for app credentials
func (t *GitHubProjectTracker) CreateApp(ctx context.Context, webhookURL string) (*AppCredentials, error) {
	// Find a free port for the callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	redirectURL := fmt.Sprintf("http://localhost:%d/callback", port)
	manifest := appManifest(webhookURL, redirectURL)

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}

	// Channel to receive the code from the callback
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	// Serve a page that auto-submits the manifest form to GitHub.
	// GitHub's manifest flow requires a POST form, not a GET with query params.
	escapedManifest := html.EscapeString(string(manifestJSON))
	formPage := fmt.Sprintf(`<!DOCTYPE html>
<html><body>
<h2>Redirecting to GitHub...</h2>
<form id="form" action="https://github.com/settings/apps/new" method="post">
  <input type="hidden" name="manifest" value="%s">
</form>
<script>document.getElementById("form").submit();</script>
</body></html>`, escapedManifest)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, formPage)
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h2>GitHub App created!</h2><p>You can close this tab and return to your terminal.</p></body></html>`)
		codeCh <- code
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", err)
		}
	}()
	defer srv.Shutdown(context.Background())

	localURL := fmt.Sprintf("http://localhost:%d", port)

	fmt.Println("  Opening browser to create GitHub App...")
	fmt.Printf("  If the browser doesn't open, visit: %s\n", localURL)

	openBrowser(localURL)

	// Wait for callback or timeout
	select {
	case code := <-codeCh:
		return t.exchangeCode(ctx, code)
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("timed out waiting for GitHub App creation (5 minutes)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// exchangeCode calls POST /app-manifests/{code}/conversions to get app credentials.
func (t *GitHubProjectTracker) exchangeCode(ctx context.Context, code string) (*AppCredentials, error) {
	cmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("/app-manifests/%s/conversions", code),
		"-X", "POST",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %s", strings.TrimSpace(string(out)))
	}

	var creds AppCredentials
	if err := json.Unmarshal(out, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse app credentials: %w", err)
	}

	return &creds, nil
}

// InstallApp installs the GitHub App on the current repo's owner.
// After creation, the user needs to install the app — this opens the install page.
func (t *GitHubProjectTracker) InstallApp(creds *AppCredentials) {
	installURL := fmt.Sprintf("%s/installations/new", creds.HTMLURL)
	fmt.Printf("  Install the app on your account/org:\n  %s\n", installURL)
	openBrowser(installURL)
}

// appCredentialsPath returns ~/.config/maestro/app.json.
func appCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "maestro", "app.json"), nil
}

// SaveAppCredentials writes the app credentials to ~/.config/maestro/app.json.
func SaveAppCredentials(creds *AppCredentials) error {
	path, err := appCredentialsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	fmt.Printf("  App credentials saved to %s\n", path)
	return nil
}

// LoadAppCredentials reads app credentials from ~/.config/maestro/app.json.
func LoadAppCredentials() (*AppCredentials, error) {
	path, err := appCredentialsPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var creds AppCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return &creds, nil
}

// CheckAppInstallation checks if the GitHub App is installed on the given owner
// by looking for a local marker file. The marker is created when the user
// confirms installation after the browser flow.
func CheckAppInstallation(ctx context.Context, creds *AppCredentials, owner string) (bool, error) {
	path, err := appCredentialsPath()
	if err != nil {
		return false, err
	}
	markerPath := filepath.Join(filepath.Dir(path), "installed-"+owner)
	_, err = os.Stat(markerPath)
	return err == nil, nil
}

// MarkAppInstalled creates a marker file indicating the app is installed on owner.
func MarkAppInstalled(owner string) error {
	path, err := appCredentialsPath()
	if err != nil {
		return err
	}
	markerPath := filepath.Join(filepath.Dir(path), "installed-"+owner)
	return os.WriteFile(markerPath, []byte("installed\n"), 0o600)
}

func openBrowser(url string) {
	// macOS
	_ = exec.Command("open", url).Start()
}
