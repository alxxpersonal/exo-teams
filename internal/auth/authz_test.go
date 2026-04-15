package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withAuthzEndpoint swaps the package-level authzEndpoint to point at srv
// and returns a restore func. Safe for sequential tests; do not run t.Parallel.
func withAuthzEndpoint(srv *httptest.Server) func() {
	prev := authzEndpoint
	authzEndpoint = srv.URL
	return func() { authzEndpoint = prev }
}

func TestExchangeSkypeToken_Success(t *testing.T) {
	const (
		rootSkypeToken    = "root-skype-jwt-placeholder"
		derivedSkypeToken = "derived-skypetoken-placeholder"
	)

	var gotMethod, gotAuth, gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"tokens":{"skypeToken":%q}}`, derivedSkypeToken)
	}))
	defer srv.Close()
	defer withAuthzEndpoint(srv)()

	got, err := ExchangeSkypeToken(rootSkypeToken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != derivedSkypeToken {
		t.Errorf("got %q, want %q", got, derivedSkypeToken)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	wantAuth := "Bearer " + rootSkypeToken
	if gotAuth != wantAuth {
		t.Errorf("Authorization = %q, want %q", gotAuth, wantAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
}

func TestExchangeSkypeToken_Errors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		contentType string
		wantErrHas  string
	}{
		{
			name:       "401 unauthorized",
			status:     http.StatusUnauthorized,
			body:       `{"error":"invalid_token"}`,
			wantErrHas: "status 401",
		},
		{
			name:       "500 server error",
			status:     http.StatusInternalServerError,
			body:       "upstream exploded",
			wantErrHas: "status 500",
		},
		{
			name:       "malformed json",
			status:     http.StatusOK,
			body:       `{"tokens":{"skypeToken":`,
			wantErrHas: "parsing authz response",
		},
		{
			name:       "empty skypetoken field",
			status:     http.StatusOK,
			body:       `{"tokens":{"skypeToken":""}}`,
			wantErrHas: "did not contain skypeToken",
		},
		{
			name:       "skypetoken field missing",
			status:     http.StatusOK,
			body:       `{"tokens":{}}`,
			wantErrHas: "did not contain skypeToken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			defer withAuthzEndpoint(srv)()

			_, err := ExchangeSkypeToken("root-token-placeholder")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErrHas)
			}
		})
	}
}

func TestExchangeSkypeToken_TransportError(t *testing.T) {
	// Point the endpoint at a closed server to force a transport-level failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	defer withAuthzEndpoint(srv)()

	_, err := ExchangeSkypeToken("root-token-placeholder")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "executing authz request") {
		t.Errorf("error = %q, want substring %q", err.Error(), "executing authz request")
	}
}
