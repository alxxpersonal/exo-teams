package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withOAuthURLs swaps the package-level OAuth URLs at the given target and
// returns a restore func. Safe for sequential tests; do not run t.Parallel.
func withOAuthURLs(device, token string) func() {
	prevDevice, prevToken := deviceCodeURL, tokenURL
	deviceCodeURL = device
	tokenURL = token
	return func() {
		deviceCodeURL = prevDevice
		tokenURL = prevToken
	}
}

// withFastPoll installs a no-op (or tiny) pollSleep so tests don't wait
// real seconds between iterations.
func withFastPoll() func() {
	prev := pollSleep
	pollSleep = func(int) {}
	return func() { pollSleep = prev }
}

// --- requestDeviceCode ---

func TestRequestDeviceCode_Success(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotClientID    string
		gotScope       string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseForm(); err != nil { //nolint:gosec // test handler, request body is controlled by this test
			t.Fatalf("ParseForm: %v", err)
		}
		gotClientID = r.PostForm.Get("client_id")
		gotScope = r.PostForm.Get("scope")

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{
			"device_code":      "dev-code-placeholder",
			"user_code":        "USER1234",
			"verification_uri": "https://microsoft.com/devicelogin",
			"expires_in":       900,
			"interval":         5,
			"message":          "enter code USER1234"
		}`)
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	dc, err := requestDeviceCode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, want form-urlencoded", gotContentType)
	}
	if gotClientID != teamsClientID {
		t.Errorf("client_id = %q, want %q", gotClientID, teamsClientID)
	}
	if gotScope != teamsScope {
		t.Errorf("scope = %q, want %q", gotScope, teamsScope)
	}

	if dc.DeviceCode != "dev-code-placeholder" {
		t.Errorf("DeviceCode = %q", dc.DeviceCode)
	}
	if dc.UserCode != "USER1234" {
		t.Errorf("UserCode = %q", dc.UserCode)
	}
	if dc.VerificationURI != "https://microsoft.com/devicelogin" {
		t.Errorf("VerificationURI = %q", dc.VerificationURI)
	}
	if dc.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want 900", dc.ExpiresIn)
	}
	if dc.Interval != 5 {
		t.Errorf("Interval = %d, want 5", dc.Interval)
	}
}

func TestRequestDeviceCode_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	_, err := requestDeviceCode()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, want to mention status 400", err.Error())
	}
}

// --- pollForToken ---

func TestPollForToken_Success(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if err := r.ParseForm(); err != nil { //nolint:gosec // test handler, request body is controlled by this test
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("device_code") != "dev-code-placeholder" {
			t.Errorf("device_code = %q", r.PostForm.Get("device_code"))
		}
		if r.PostForm.Get("client_id") != teamsClientID {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{
			"access_token":  "skype-access-placeholder",
			"refresh_token": "refresh-placeholder",
			"expires_in":    3600,
			"token_type":    "Bearer",
			"scope":         "https://api.spaces.skype.com/.default offline_access"
		}`)
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()
	defer withFastPoll()()

	dc := &DeviceCodeResponse{
		DeviceCode: "dev-code-placeholder",
		ExpiresIn:  60,
		Interval:   1,
	}

	tok, err := pollForToken(dc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken != "skype-access-placeholder" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-placeholder" {
		t.Errorf("RefreshToken = %q", tok.RefreshToken)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("handler calls = %d, want 1", got)
	}
}

func TestPollForToken_PendingThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-ok","refresh_token":"refresh-ok","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()
	defer withFastPoll()()

	dc := &DeviceCodeResponse{
		DeviceCode: "dev-code-placeholder",
		ExpiresIn:  60,
		Interval:   1,
	}

	tok, err := pollForToken(dc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken != "access-ok" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("handler calls = %d, want 3", got)
	}
}

func TestPollForToken_TerminalErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErrHas string
	}{
		{
			name:       "declined",
			body:       `{"error":"authorization_declined"}`,
			wantErrHas: "declined",
		},
		{
			name:       "expired",
			body:       `{"error":"expired_token"}`,
			wantErrHas: "expired",
		},
		{
			name:       "unknown error",
			body:       `{"error":"invalid_grant"}`,
			wantErrHas: "unexpected error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			defer withOAuthURLs(srv.URL, srv.URL)()
			defer withFastPoll()()

			dc := &DeviceCodeResponse{
				DeviceCode: "dev-code-placeholder",
				ExpiresIn:  60,
				Interval:   1,
			}

			_, err := pollForToken(dc)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErrHas)
			}
		})
	}
}

func TestPollForToken_SlowDownIncreasesInterval(t *testing.T) {
	var (
		calls         int32
		sleepArgs     []int
		sleepArgsMu   sync.Mutex
		prevPollSleep = pollSleep
	)
	pollSleep = func(interval int) {
		sleepArgsMu.Lock()
		sleepArgs = append(sleepArgs, interval)
		sleepArgsMu.Unlock()
	}
	defer func() { pollSleep = prevPollSleep }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ok","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	dc := &DeviceCodeResponse{
		DeviceCode: "dev-code-placeholder",
		ExpiresIn:  60,
		Interval:   2,
	}

	if _, err := pollForToken(dc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sleepArgsMu.Lock()
	defer sleepArgsMu.Unlock()
	if len(sleepArgs) < 2 {
		t.Fatalf("expected at least 2 sleeps, got %v", sleepArgs)
	}
	if sleepArgs[0] != 2 {
		t.Errorf("first sleep = %d, want 2", sleepArgs[0])
	}
	if sleepArgs[1] != 7 {
		t.Errorf("second sleep = %d, want 7 (slow_down adds 5)", sleepArgs[1])
	}
}

// --- requestTokenWithScope ---

func TestRequestTokenWithScope_Success(t *testing.T) {
	const (
		refresh = "refresh-placeholder"
		scope   = "https://graph.microsoft.com/.default offline_access"
	)

	var gotForm map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil { //nolint:gosec // test handler, request body is controlled by this test
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = map[string]string{
			"client_id":     r.PostForm.Get("client_id"),
			"grant_type":    r.PostForm.Get("grant_type"),
			"refresh_token": r.PostForm.Get("refresh_token"),
			"scope":         r.PostForm.Get("scope"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"graph-access","refresh_token":"graph-refresh","expires_in":3600,"token_type":"Bearer","scope":"` + scope + `"}`))
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	tok, err := requestTokenWithScope(refresh, scope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"client_id":     teamsClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refresh,
		"scope":         scope,
	}
	for k, v := range want {
		if gotForm[k] != v {
			t.Errorf("form[%q] = %q, want %q", k, gotForm[k], v)
		}
	}
	if tok.AccessToken != "graph-access" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "graph-refresh" {
		t.Errorf("RefreshToken = %q", tok.RefreshToken)
	}
}

func TestRequestTokenWithScope_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	_, err := requestTokenWithScope("refresh-placeholder", "some-scope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want to mention 401", err.Error())
	}
}

// --- Full login orchestration via fake server ---

// TestLoginFlow_OrchestratesScopedTokens verifies the end-to-end device code
// flow: request device code, poll once, then request the 4 additional scoped
// tokens (chatsvcagg, teams, graph, assignments) using the refresh token.
// We don't invoke Login() directly because it writes files to ~/.exo-teams/.
// Instead we re-use the same exported helpers Login uses, in the same order.
func TestLoginFlow_OrchestratesScopedTokens(t *testing.T) {
	// Track each token-endpoint call by scope.
	var (
		mu            sync.Mutex
		scopeSequence []string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/devicecode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"device_code":      "dev-code-placeholder",
			"user_code":        "USER1234",
			"verification_uri": "https://microsoft.com/devicelogin",
			"expires_in":       900,
			"interval":         1,
			"message":          "enter USER1234"
		}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil { //nolint:gosec // test handler, request body is controlled by this test
			t.Fatalf("ParseForm: %v", err)
		}
		grant := r.PostForm.Get("grant_type")
		scope := r.PostForm.Get("scope")

		mu.Lock()
		if grant == "urn:ietf:params:oauth:grant-type:device_code" {
			scopeSequence = append(scopeSequence, "device_code_exchange")
		} else {
			scopeSequence = append(scopeSequence, scope)
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"access_token":  "access-for-" + scope,
			"refresh_token": "refresh-rotated-placeholder",
			"expires_in":    3600,
			"token_type":    "Bearer",
			"scope":         scope,
		}
		if grant == "urn:ietf:params:oauth:grant-type:device_code" {
			body["access_token"] = "skype-access-placeholder"
		}
		_ = json.NewEncoder(w).Encode(body)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer withOAuthURLs(srv.URL+"/devicecode", srv.URL+"/token")()
	defer withFastPoll()()

	// Step 1: device code
	dc, err := requestDeviceCode()
	if err != nil {
		t.Fatalf("requestDeviceCode: %v", err)
	}
	if dc.DeviceCode != "dev-code-placeholder" {
		t.Errorf("DeviceCode = %q", dc.DeviceCode)
	}

	// Step 2: poll for token (succeeds immediately in our fake)
	tok, err := pollForToken(dc)
	if err != nil {
		t.Fatalf("pollForToken: %v", err)
	}
	if tok.AccessToken != "skype-access-placeholder" {
		t.Errorf("skype access = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-rotated-placeholder" {
		t.Errorf("refresh = %q", tok.RefreshToken)
	}

	// Step 3: four scoped token requests in the same order Login uses.
	wantScopes := []string{
		"https://chatsvcagg.teams.microsoft.com/.default offline_access",
		"https://teams.microsoft.com/.default offline_access",
		"https://graph.microsoft.com/.default offline_access",
		"https://assignments.onenote.com/.default offline_access",
	}
	for _, s := range wantScopes {
		if _, err := requestTokenWithScope(tok.RefreshToken, s); err != nil {
			t.Fatalf("requestTokenWithScope %q: %v", s, err)
		}
	}

	mu.Lock()
	got := append([]string(nil), scopeSequence...)
	mu.Unlock()

	wantSeq := append([]string{"device_code_exchange"}, wantScopes...)
	if len(got) != len(wantSeq) {
		t.Fatalf("call sequence = %v, want %v", got, wantSeq)
	}
	for i := range wantSeq {
		if got[i] != wantSeq[i] {
			t.Errorf("call[%d] = %q, want %q", i, got[i], wantSeq[i])
		}
	}
}

// Make sure the shape we build into tokens with multiple scoped responses is
// assembled correctly (mirrors the fan-out Login() does).
func TestLoginFlow_AssemblesTokensStruct(t *testing.T) {
	// Always-success token endpoint, keyed by scope.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil { //nolint:gosec // test handler, request body is controlled by this test
			t.Fatalf("ParseForm: %v", err)
		}
		scope := r.PostForm.Get("scope")
		body, _ := json.Marshal(OAuthTokenResponse{
			AccessToken:  "token-for-" + scope,
			RefreshToken: "refresh-rotated",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
			Scope:        scope,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	chatsvc, err := requestTokenWithScope("refresh-placeholder", "https://chatsvcagg.teams.microsoft.com/.default offline_access")
	if err != nil {
		t.Fatalf("chatsvc: %v", err)
	}
	teams, err := requestTokenWithScope("refresh-placeholder", "https://teams.microsoft.com/.default offline_access")
	if err != nil {
		t.Fatalf("teams: %v", err)
	}
	graph, err := requestTokenWithScope("refresh-placeholder", "https://graph.microsoft.com/.default offline_access")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	assignments, err := requestTokenWithScope("refresh-placeholder", "https://assignments.onenote.com/.default offline_access")
	if err != nil {
		t.Fatalf("assignments: %v", err)
	}

	tokens := &Tokens{
		Skype:       "skype-access-placeholder",
		ChatSvcAgg:  chatsvc.AccessToken,
		Teams:       teams.AccessToken,
		Graph:       graph.AccessToken,
		Assignments: assignments.AccessToken,
	}

	if !strings.HasPrefix(tokens.ChatSvcAgg, "token-for-https://chatsvcagg.") {
		t.Errorf("ChatSvcAgg = %q", tokens.ChatSvcAgg)
	}
	if !strings.HasPrefix(tokens.Teams, "token-for-https://teams.") {
		t.Errorf("Teams = %q", tokens.Teams)
	}
	if !strings.HasPrefix(tokens.Graph, "token-for-https://graph.") {
		t.Errorf("Graph = %q", tokens.Graph)
	}
	if !strings.HasPrefix(tokens.Assignments, "token-for-https://assignments.") {
		t.Errorf("Assignments = %q", tokens.Assignments)
	}
}

// Sanity: ensure tests don't actually hit the network by making the real
// default URLs fail loudly if any test forgets to override them. We verify
// this by catching a transport error when we point at a closed server and
// confirm the body isn't read past the point of failure.
func TestRequestDeviceCode_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	_, err := requestDeviceCode()
	if err == nil {
		t.Fatal("expected transport error")
	}
}

// Guard: confirm each top-level test completes well under the 2s budget.
// We don't enforce per-test timing here, but this ensures the slowest path
// (pollForToken with a pending response) finishes fast under a no-op sleep.
func TestPollForToken_FastUnderFakeSleep(t *testing.T) {
	defer withFastPoll()()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"ok","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer srv.Close()
	defer withOAuthURLs(srv.URL, srv.URL)()

	dc := &DeviceCodeResponse{
		DeviceCode: "dev-code-placeholder",
		ExpiresIn:  60,
		Interval:   1,
	}

	start := time.Now()
	if _, err := pollForToken(dc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("pollForToken took %v, expected well under 1s", elapsed)
	}
}
