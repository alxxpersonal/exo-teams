package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	// Teams desktop client ID (public client, no secret needed)
	teamsClientID = "1fec8e78-bce4-4aaf-ab1b-5451cc387264"
	// Scopes needed for Teams internal API access
	teamsScope    = "https://api.spaces.skype.com/.default offline_access"
	tokenURL      = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	deviceCodeURL = "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode"
)

// OAuthTokenResponse is the response from the OAuth token endpoint.
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// DeviceCodeResponse is the response from the device code endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

// Login performs device code flow:
// 1. Request device code from Microsoft
// 2. User opens browser and enters code
// 3. Poll for token completion
// 4. Exchange for all needed tokens
// 5. Save to ~/.exo-teams/
func Login() error {
	fmt.Fprintln(os.Stderr, "requesting device code...")

	// Step 1: Get device code
	deviceCode, err := requestDeviceCode()
	if err != nil {
		return fmt.Errorf("device code request failed: %w", err)
	}

	// Step 2: Show user the code and open browser
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, deviceCode.Message)
	fmt.Fprintln(os.Stderr)

	openBrowser(deviceCode.VerificationURI)

	// Step 3: Poll for token
	fmt.Fprintln(os.Stderr, "waiting for authentication...")
	oauthTokens, err := pollForToken(deviceCode)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "authenticated, exchanging for Teams tokens...")

	// The access_token from the skype scope IS the skype token
	skypeToken := oauthTokens.AccessToken

	// Verify skypetoken exchange works
	_, err = ExchangeSkypeToken(skypeToken)
	if err != nil {
		return fmt.Errorf("skypetoken exchange failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "skypetoken exchange successful")

	// Request chatsvcagg token
	chatsvcTokens, err := requestTokenWithScope(oauthTokens.RefreshToken, "https://chatsvcagg.teams.microsoft.com/.default offline_access")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not get chatsvcagg token: %v\n", err)
		chatsvcTokens = &OAuthTokenResponse{AccessToken: skypeToken}
	}

	// Request teams token
	teamsTokens, err := requestTokenWithScope(oauthTokens.RefreshToken, "https://teams.microsoft.com/.default offline_access")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not get teams token: %v\n", err)
		teamsTokens = &OAuthTokenResponse{AccessToken: skypeToken}
	}

	// Request Graph token (for calendar, files, search)
	fmt.Fprintln(os.Stderr, "requesting graph token...")
	graphTokens, err := requestTokenWithScope(oauthTokens.RefreshToken, "https://graph.microsoft.com/.default offline_access")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not get graph token: %v\n", err)
		graphTokens = &OAuthTokenResponse{}
	}

	// Request assignments token (for education assignments - bypasses admin consent)
	fmt.Fprintln(os.Stderr, "requesting assignments token...")
	assignmentsTokens, err := requestTokenWithScope(oauthTokens.RefreshToken, "https://assignments.onenote.com/.default offline_access")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not get assignments token: %v\n", err)
		assignmentsTokens = &OAuthTokenResponse{}
	}

	// Save tokens
	tokens := &Tokens{
		Skype:       skypeToken,
		ChatSvcAgg:  chatsvcTokens.AccessToken,
		Teams:       teamsTokens.AccessToken,
		Graph:       graphTokens.AccessToken,
		Assignments: assignmentsTokens.AccessToken,
	}

	if err := Save(tokens); err != nil {
		return fmt.Errorf("saving tokens: %w", err)
	}

	// Save refresh token for future refresh
	if oauthTokens.RefreshToken != "" {
		dir, _ := TokenDir()
		_ = writeTokenFile(dir+"/refresh-token.jwt", oauthTokens.RefreshToken)
	}

	fmt.Fprintln(os.Stderr, "tokens saved to ~/.exo-teams/")
	return CheckTokens()
}

func requestDeviceCode() (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {teamsClientID},
		"scope":     {teamsScope},
	}

	resp, err := http.PostForm(deviceCodeURL, data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var dcResp DeviceCodeResponse
	if err := json.Unmarshal(body, &dcResp); err != nil {
		return nil, err
	}

	return &dcResp, nil
}

func pollForToken(dc *DeviceCodeResponse) (*OAuthTokenResponse, error) {
	interval := dc.Interval
	if interval == 0 {
		interval = 5
	}

	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		data := url.Values{
			"client_id":   {teamsClientID},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {dc.DeviceCode},
		}

		resp, err := http.PostForm(tokenURL, data)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var tokenResp OAuthTokenResponse
			if err := json.Unmarshal(body, &tokenResp); err != nil {
				return nil, err
			}
			return &tokenResp, nil
		}

		// Check if still pending or denied
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &errResp)

		switch errResp.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			return nil, fmt.Errorf("device code expired - try again")
		case "authorization_declined":
			return nil, fmt.Errorf("authorization was declined")
		default:
			return nil, fmt.Errorf("unexpected error: %s", string(body))
		}
	}

	return nil, fmt.Errorf("timed out waiting for authentication")
}

func requestTokenWithScope(refreshToken, scope string) (*OAuthTokenResponse, error) {
	data := url.Values{
		"client_id":     {teamsClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {scope},
	}

	return postTokenRequest(data)
}

func postTokenRequest(data url.Values) (*OAuthTokenResponse, error) {
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	return &tokenResp, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

// RefreshTokens uses saved refresh token to get fresh access tokens.
func RefreshTokens() error {
	dir, err := TokenDir()
	if err != nil {
		return err
	}

	refreshToken, err := readTokenFile(dir + "/refresh-token.jwt")
	if err != nil {
		return fmt.Errorf("no refresh token found - run 'exo-teams auth' to login again")
	}

	refreshToken = strings.TrimSpace(refreshToken)

	skypeResp, err := requestTokenWithScope(refreshToken, teamsScope)
	if err != nil {
		return fmt.Errorf("refreshing skype token: %w", err)
	}

	chatsvcResp, err := requestTokenWithScope(refreshToken, "https://chatsvcagg.teams.microsoft.com/.default offline_access")
	if err != nil {
		chatsvcResp = &OAuthTokenResponse{AccessToken: skypeResp.AccessToken}
	}

	teamsResp, err := requestTokenWithScope(refreshToken, "https://teams.microsoft.com/.default offline_access")
	if err != nil {
		teamsResp = &OAuthTokenResponse{AccessToken: skypeResp.AccessToken}
	}

	graphResp, err := requestTokenWithScope(refreshToken, "https://graph.microsoft.com/.default offline_access")
	if err != nil {
		graphResp = &OAuthTokenResponse{}
	}

	assignmentsResp, err := requestTokenWithScope(refreshToken, "https://assignments.onenote.com/.default offline_access")
	if err != nil {
		assignmentsResp = &OAuthTokenResponse{}
	}

	tokens := &Tokens{
		Skype:       skypeResp.AccessToken,
		ChatSvcAgg:  chatsvcResp.AccessToken,
		Teams:       teamsResp.AccessToken,
		Graph:       graphResp.AccessToken,
		Assignments: assignmentsResp.AccessToken,
	}

	if err := Save(tokens); err != nil {
		return fmt.Errorf("saving refreshed tokens: %w", err)
	}

	if skypeResp.RefreshToken != "" {
		_ = writeTokenFile(dir+"/refresh-token.jwt", skypeResp.RefreshToken)
	}

	fmt.Fprintln(os.Stderr, "tokens refreshed")
	return CheckTokens()
}
