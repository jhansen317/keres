package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	tokenFile = "token.json"
	credsFile = "credentials.json"
)

// getConfigDir returns the directory where config files are stored
func getConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configDir := filepath.Join(home, ".keres")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}
	return configDir, nil
}

// getTokenPath returns the full path to the token file
func getTokenPath() (string, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, tokenFile), nil
}

// getCredsPath returns the full path to the credentials file
func getCredsPath() (string, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, credsFile), nil
}

// GetGoogleClient creates an authenticated HTTP client for Google APIs
func GetGoogleClient(scopes []string) (*http.Client, error) {
	ctx := context.Background()

	// Read credentials file
	credsPath, err := getCredsPath()
	if err != nil {
		return nil, err
	}

	b, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read client secret file: %w\n\n"+
			"Please create a Google Cloud project and download OAuth2 credentials:\n"+
			"1. Go to https://console.cloud.google.com/\n"+
			"2. Create a new project or select an existing one\n"+
			"3. Enable Gmail API and Google Drive API\n"+
			"4. Create OAuth2 credentials (Desktop app)\n"+
			"5. Download the credentials and save as: %s", err, credsPath)
	}

	config, err := google.ConfigFromJSON(b, scopes...)
	if err != nil {
		return nil, fmt.Errorf("unable to parse client secret file: %w", err)
	}

	return getClient(ctx, config)
}

// getClient retrieves a token from file or initiates OAuth flow
func getClient(ctx context.Context, config *oauth2.Config) (*http.Client, error) {
	tokenPath, err := getTokenPath()
	if err != nil {
		return nil, err
	}

	tok, err := tokenFromFile(tokenPath)
	if err != nil {
		tok, err = getTokenFromWeb(config)
		if err != nil {
			return nil, err
		}
		saveToken(tokenPath, tok)
	}

	return config.Client(ctx, tok), nil
}

// getTokenFromWeb initiates OAuth2 flow
func getTokenFromWeb(config *oauth2.Config) (*oauth2.Token, error) {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser:\n\n%v\n\n", authURL)
	fmt.Print("Enter the authorization code: ")

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		return nil, fmt.Errorf("unable to read authorization code: %w", err)
	}

	tok, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve token from web: %w", err)
	}
	return tok, nil
}

// tokenFromFile retrieves a token from a local file
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// saveToken saves a token to a file
func saveToken(path string, token *oauth2.Token) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("unable to cache oauth token: %w", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(token)
}

// CheckAuthStatus checks if user is authenticated
func CheckAuthStatus() (bool, string, error) {
	tokenPath, err := getTokenPath()
	if err != nil {
		return false, "", err
	}

	tok, err := tokenFromFile(tokenPath)
	if err != nil {
		return false, "", nil // Not authenticated, not an error
	}

	// Try to get user email via userinfo endpoint
	ctx := context.Background()
	credsPath, _ := getCredsPath()
	b, err := os.ReadFile(credsPath)
	if err != nil {
		return true, "authenticated user", nil
	}

	config, err := google.ConfigFromJSON(b)
	if err != nil {
		return true, "authenticated user", nil
	}

	client := config.Client(ctx, tok)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return true, "authenticated user", nil
	}
	defer resp.Body.Close()

	var info struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.Email == "" {
		return true, "authenticated user", nil
	}

	return true, info.Email, nil
}

// Logout removes cached credentials
func Logout() error {
	tokenPath, err := getTokenPath()
	if err != nil {
		return err
	}

	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove token file: %w", err)
	}

	return nil
}
