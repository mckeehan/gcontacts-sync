package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// NewOAuthClient returns an authenticated *http.Client using the OAuth2 credentials file.
// On first run it will open a browser to complete the OAuth flow and cache the token.
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
		// First run – perform interactive OAuth flow
		tok, err = getTokenFromWeb(ctx, cfg)
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

func getTokenFromWeb(ctx context.Context, cfg *oauth2.Config) (*oauth2.Token, error) {
	cfg.RedirectURL = "urn:ietf:wg:oauth:2.0:oob"
	authURL := cfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline)

	fmt.Printf("\nOpen this URL in your browser to authorise access:\n\n  %s\n\nEnter the authorisation code: ", authURL)

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		return nil, fmt.Errorf("reading auth code: %w", err)
	}

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchanging auth code: %w", err)
	}
	return tok, nil
}
