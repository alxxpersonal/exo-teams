package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Note: RefreshTokens is defined in oauth.go

const (
	tokenDir              = ".exo-teams"
	skypeTokenFile        = "token-skype.jwt"
	chatsvcTokenFile      = "token-chatsvcagg.jwt"
	teamsTokenFile        = "token-teams.jwt"
	graphTokenFile        = "token-graph.jwt"
	assignmentsTokenFile  = "token-assignments.jwt"
)

// Tokens holds all the authentication tokens needed for Teams API access.
type Tokens struct {
	Skype       string // root skype token (used to derive skypetoken via authz)
	ChatSvcAgg  string // chat service aggregation token (for teams/channels listing)
	Teams       string // teams middle tier token
	Graph       string // microsoft graph token (calendar, files, search)
	Assignments string // assignments.onenote.com token (education assignments)
}

// TokenDir returns the path to the token storage directory.
func TokenDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, tokenDir), nil
}

// Load reads all tokens from disk. Returns an error if any token file is missing.
func Load() (*Tokens, error) {
	dir, err := TokenDir()
	if err != nil {
		return nil, err
	}

	skype, err := readTokenFile(filepath.Join(dir, skypeTokenFile))
	if err != nil {
		return nil, fmt.Errorf("reading skype token: %w", err)
	}

	chatsvc, err := readTokenFile(filepath.Join(dir, chatsvcTokenFile))
	if err != nil {
		return nil, fmt.Errorf("reading chatsvcagg token: %w", err)
	}

	teams, err := readTokenFile(filepath.Join(dir, teamsTokenFile))
	if err != nil {
		return nil, fmt.Errorf("reading teams token: %w", err)
	}

	graph, _ := readTokenFile(filepath.Join(dir, graphTokenFile))             // optional
	assignments, _ := readTokenFile(filepath.Join(dir, assignmentsTokenFile)) // optional

	return &Tokens{
		Skype:       skype,
		ChatSvcAgg:  chatsvc,
		Teams:       teams,
		Graph:       graph,
		Assignments: assignments,
	}, nil
}

// Save writes all tokens to disk.
func Save(tokens *Tokens) error {
	dir, err := TokenDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating token directory: %w", err)
	}

	if err := writeTokenFile(filepath.Join(dir, skypeTokenFile), tokens.Skype); err != nil {
		return fmt.Errorf("writing skype token: %w", err)
	}
	if err := writeTokenFile(filepath.Join(dir, chatsvcTokenFile), tokens.ChatSvcAgg); err != nil {
		return fmt.Errorf("writing chatsvcagg token: %w", err)
	}
	if err := writeTokenFile(filepath.Join(dir, teamsTokenFile), tokens.Teams); err != nil {
		return fmt.Errorf("writing teams token: %w", err)
	}

	if tokens.Graph != "" {
		if err := writeTokenFile(filepath.Join(dir, graphTokenFile), tokens.Graph); err != nil {
			return fmt.Errorf("writing graph token: %w", err)
		}
	}

	if tokens.Assignments != "" {
		if err := writeTokenFile(filepath.Join(dir, assignmentsTokenFile), tokens.Assignments); err != nil {
			return fmt.Errorf("writing assignments token: %w", err)
		}
	}

	return nil
}

// IsExpired checks if a JWT token has expired (with a 60-second buffer).
func IsExpired(token string) (bool, error) {
	exp, err := getTokenExpiry(token)
	if err != nil {
		return true, err
	}
	return time.Now().After(exp.Add(-60 * time.Second)), nil
}

// GetTokenExpiry returns the expiration time of a JWT.
func GetTokenExpiry(token string) (time.Time, error) {
	return getTokenExpiry(token)
}

func getTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard encoding as fallback
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return time.Time{}, fmt.Errorf("decoding JWT payload: %w", err)
		}
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parsing JWT claims: %w", err)
	}

	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim in token")
	}

	return time.Unix(claims.Exp, 0), nil
}

// EnsureFresh checks if tokens are expired and refreshes them if a refresh token exists.
// Returns the (possibly refreshed) tokens.
func EnsureFresh(tokens *Tokens) (*Tokens, error) {
	// Check if the chatsvcagg token is expired (it's the most commonly used)
	expired, err := IsExpired(tokens.ChatSvcAgg)
	if err != nil || !expired {
		// Either can't check or not expired, return as-is
		return tokens, nil
	}

	// Token is expired, try to refresh
	dir, err := TokenDir()
	if err != nil {
		return tokens, nil // can't find dir, return original
	}

	refreshToken, err := readTokenFile(dir + "/refresh-token.jwt")
	if err != nil || refreshToken == "" {
		// No refresh token available
		return tokens, fmt.Errorf("tokens expired and no refresh token available - run 'exo-teams auth'")
	}

	fmt.Fprintf(os.Stderr, "tokens expired, refreshing...\n")
	if err := RefreshTokens(); err != nil {
		return tokens, fmt.Errorf("auto-refresh failed: %w", err)
	}

	// Reload fresh tokens
	fresh, err := Load()
	if err != nil {
		return tokens, fmt.Errorf("loading refreshed tokens: %w", err)
	}

	return fresh, nil
}

func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeTokenFile(path string, token string) error {
	return os.WriteFile(path, []byte(token+"\n"), 0600)
}
