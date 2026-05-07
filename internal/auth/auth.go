package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// NewOAuthClient returns an authenticated *http.Client using the OAuth2 credentials file.
// On first run it starts a local HTTP server to receive the OAuth redirect.
func NewOAuthClient(ctx context.Context, credFile, tokenFile string) (*http.Client, error) {
	data, err := os.ReadFile(credFile)
	if err != nil {
		return nil, fmt.Errorf("reading credentials file %q: %w\n\nDownload credentials.json from:\n  https://console.cloud.google.com/apis/credentials", credFile, err)
	}

	cfg, err := google.ConfigFromJSON(data,
		"https://www.googleapis.com/auth/contacts",
	)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}

	tok, err := loadToken(tokenFile)
	if err != nil {
		tok, err = getTokenViaLocalServer(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if err := saveToken(tokenFile, tok); err != nil {
			return nil, err
		}
	}

	return cfg.Client(ctx, tok), nil
}

func loadToken(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	return tok, json.NewDecoder(f).Decode(tok)
}

func saveToken(path string, tok *oauth2.Token) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating token file %q: %w", path, err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(tok)
}

// getTokenViaLocalServer starts a temporary HTTP server on a random local port,
// opens the OAuth consent URL in the browser, waits for the redirect, and
// exchanges the code for a token. This replaces the deprecated OOB flow.
func getTokenViaLocalServer(ctx context.Context, cfg *oauth2.Config) (*oauth2.Token, error) {
	// Listen on a random available port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting local OAuth server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)
	cfg.RedirectURL = redirectURL

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{}
	http.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in OAuth callback: %s", r.URL.RawQuery)
			fmt.Fprintln(w, "Error: no authorisation code received. You can close this tab.")
			return
		}
		codeCh <- code
		fmt.Fprintln(w, "Authorisation complete. You can close this tab and return to the terminal.")
	})

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	authURL := cfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("\nOpening browser for Google OAuth authorisation...\n")
	fmt.Printf("If the browser does not open, visit:\n\n  %s\n\n", authURL)
	openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		srv.Close()
		return nil, err
	case <-ctx.Done():
		srv.Close()
		return nil, ctx.Err()
	}
	srv.Close()

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchanging auth code: %w", err)
	}
	return tok, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
