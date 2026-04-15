package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// authzEndpoint is the Teams authz URL. Declared as a var so tests can
// override it to point at a fake server. Default is the real endpoint.
var authzEndpoint = "https://teams.microsoft.com/api/authsvc/v1.0/authz"

// AuthzResponse is the response from the authz endpoint.
type AuthzResponse struct {
	Tokens struct {
		SkypeToken string `json:"skypeToken"`
	} `json:"tokens"`
}

// ExchangeSkypeToken exchanges the root skype token for a derived skypetoken
// via the Teams authz endpoint.
func ExchangeSkypeToken(skypeToken string) (string, error) {
	req, err := http.NewRequest("POST", authzEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("creating authz request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+skypeToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing authz request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading authz response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authz returned status %d: %s", resp.StatusCode, string(body))
	}

	var authzResp AuthzResponse
	if err := json.Unmarshal(body, &authzResp); err != nil {
		return "", fmt.Errorf("parsing authz response: %w", err)
	}

	if authzResp.Tokens.SkypeToken == "" {
		return "", fmt.Errorf("authz response did not contain skypeToken")
	}

	return authzResp.Tokens.SkypeToken, nil
}
