package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

	tokens, err = auth.EnsureFresh(tokens)
	if err != nil {
		return nil, err
	}

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

// sanitizeURL returns a URL string with any query string and fragment stripped,
// suitable for logging without leaking tokens or identifiers embedded in query params.
func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clean := *u
	clean.RawQuery = ""
	clean.Fragment = ""
	return clean.String()
}

// doRequest executes an HTTP request and returns the response body.
// Retries on transient failures, auto-refreshes tokens on 401, and uses ctx for cancellation.
// Errors never include the response body to avoid leaking tokens or sensitive data.
func (c *Client) doRequest(ctx context.Context, req *http.Request) ([]byte, error) {
	_, body, err := c.doRawRequest(ctx, req)
	return body, err
}

// doRequestJSON executes an HTTP request and unmarshals the JSON response.
func (c *Client) doRequestJSON(ctx context.Context, req *http.Request, target any) error {
	body, err := c.doRequest(ctx, req)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("parsing JSON response from %s %s: %w", req.Method, sanitizeURL(req.URL), err)
	}

	return nil
}

// doRawRequest executes an HTTP request and returns both the response headers
// and the fully read body. Retries on transient failures, auto-refreshes tokens
// on 401, and uses ctx for cancellation. The response Body is closed before
// returning. Errors never include the response body to avoid leaking tokens.
func (c *Client) doRawRequest(ctx context.Context, req *http.Request) (http.Header, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var bodyBytes []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("reading request body for %s %s: %w", req.Method, sanitizeURL(req.URL), err)
		}
		_ = req.Body.Close()
		bodyBytes = b
	}
	reqURL := req.URL
	method := req.Method
	headers := req.Header.Clone()

	build := func() (*http.Request, error) {
		var body io.Reader
		if bodyBytes != nil {
			body = bytes.NewReader(bodyBytes)
		}
		r, err := http.NewRequestWithContext(ctx, method, reqURL.String(), body)
		if err != nil {
			return nil, err
		}
		r.Header = headers.Clone()
		return r, nil
	}

	resp, err := retryDo(retryDoer{ctx: ctx, client: c.http, buildReq: build})
	if err != nil {
		return nil, nil, fmt.Errorf("executing %s %s: %w", method, sanitizeURL(reqURL), err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if refreshed, rerr := c.refreshFor(headers); rerr == nil && refreshed {
			resp, err = retryDo(retryDoer{ctx: ctx, client: c.http, buildReq: build})
			if err != nil {
				return nil, nil, fmt.Errorf("executing %s %s: %w", method, sanitizeURL(reqURL), err)
			}
		} else {
			return nil, nil, fmt.Errorf("%s %s returned status %d", method, sanitizeURL(reqURL), http.StatusUnauthorized)
		}
	}

	respHeaders := resp.Header.Clone()
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("reading response from %s %s: %w", method, sanitizeURL(reqURL), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("%s %s returned status %d", method, sanitizeURL(reqURL), resp.StatusCode)
	}

	return respHeaders, body, nil
}

// refreshFor refreshes the tokens referenced by the request headers and
// rewrites them in place so the retry carries the new credentials.
// Returns true when the headers were successfully updated.
func (c *Client) refreshFor(headers http.Header) (bool, error) {
	fresh, err := auth.EnsureFresh(c.tokens)
	if err != nil {
		return false, err
	}
	c.tokens = fresh

	if authn := headers.Get("Authentication"); strings.HasPrefix(authn, "skypetoken=") {
		newSkype, err := auth.ExchangeSkypeToken(c.tokens.Skype)
		if err != nil {
			return false, fmt.Errorf("exchanging skype token: %w", err)
		}
		c.skypeToken = newSkype
		headers.Set("Authentication", "skypetoken="+newSkype)
		return true, nil
	}

	if bearer := headers.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
		// Replace with the most plausible fresh bearer. The client uses ChatSvcAgg
		// for Teams middle tier requests and Graph for graph endpoints.
		switch {
		case strings.Contains(bearer, c.tokens.ChatSvcAgg) || bearer == "Bearer "+c.tokens.ChatSvcAgg:
			headers.Set("Authorization", "Bearer "+c.tokens.ChatSvcAgg)
		case strings.Contains(bearer, c.tokens.Graph) || bearer == "Bearer "+c.tokens.Graph:
			headers.Set("Authorization", "Bearer "+c.tokens.Graph)
		default:
			headers.Set("Authorization", "Bearer "+c.tokens.ChatSvcAgg)
		}
		return true, nil
	}

	return false, nil
}
