package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alxxpersonal/exo-teams/internal/auth"
)

const (
	teamsBaseURL = "https://teams.microsoft.com/api/csa/api/v1"
	msgBaseURL   = "https://emea.ng.msg.teams.microsoft.com/v1"
)

// Client is the Teams internal API client.
type Client struct {
	http       *http.Client
	tokens     *auth.Tokens
	skypeToken string // derived skypetoken from authz exchange
}

// NewClient creates a new API client with loaded tokens.
// Automatically refreshes tokens if they are expired.
func NewClient() (*Client, error) {
	tokens, err := auth.Load()
	if err != nil {
		return nil, fmt.Errorf("loading tokens: %w", err)
	}

	// Auto-refresh if expired
	tokens, err = auth.EnsureFresh(tokens)
	if err != nil {
		return nil, err
	}

	// Exchange the root skype token for a derived skypetoken
	skypeToken, err := auth.ExchangeSkypeToken(tokens.Skype)
	if err != nil {
		return nil, fmt.Errorf("exchanging skype token: %w", err)
	}

	return &Client{
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		tokens:     tokens,
		skypeToken: skypeToken,
	}, nil
}

// NewClientWithoutExchange creates a client that only uses the chatsvcagg token
// (for endpoints that don't need skypetoken like list-teams).
// Automatically refreshes tokens if they are expired.
func NewClientWithoutExchange() (*Client, error) {
	tokens, err := auth.Load()
	if err != nil {
		return nil, fmt.Errorf("loading tokens: %w", err)
	}

	// Auto-refresh if expired
	tokens, err = auth.EnsureFresh(tokens)
	if err != nil {
		return nil, err
	}

	return &Client{
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		tokens: tokens,
	}, nil
}

// Tokens returns the client's auth tokens (for use by commands that need graph token etc).
func (c *Client) Tokens() *auth.Tokens {
	return c.tokens
}

// EnsureSkypeToken ensures the derived skypetoken is available, exchanging if needed.
func (c *Client) EnsureSkypeToken() error {
	if c.skypeToken != "" {
		return nil
	}

	skypeToken, err := auth.ExchangeSkypeToken(c.tokens.Skype)
	if err != nil {
		return fmt.Errorf("exchanging skype token: %w", err)
	}
	c.skypeToken = skypeToken
	return nil
}

// getUserObjectID extracts the "oid" (object ID) claim from the Skype JWT token.
// This is used to identify the current user in API requests (e.g., as "8:orgid:<oid>").
func (c *Client) getUserObjectID() (string, error) {
	parts := strings.Split(c.tokens.Skype, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid skype JWT: expected 3 parts, got %d", len(parts))
	}

	payload := parts[1]
	// Add base64 padding if needed.
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", fmt.Errorf("decoding skype JWT payload: %w", err)
		}
	}

	var claims struct {
		OID string `json:"oid"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", fmt.Errorf("parsing skype JWT claims: %w", err)
	}

	if claims.OID == "" {
		return "", fmt.Errorf("no oid claim in skype token")
	}

	return claims.OID, nil
}

// doRequest executes an HTTP request and returns the response body.
func (c *Client) doRequest(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request to %s: %w", req.URL.String(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", req.URL.String(), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request to %s returned status %d: %s", req.URL.String(), resp.StatusCode, string(body))
	}

	return body, nil
}

// doRequestJSON executes an HTTP request and unmarshals the JSON response.
func (c *Client) doRequestJSON(req *http.Request, target any) error {
	body, err := c.doRequest(req)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("parsing JSON response from %s: %w", req.URL.String(), err)
	}

	return nil
}
