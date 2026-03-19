package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeJWT creates a minimal JWT with the given claims for testing.
func makeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s.%s.sig", header, payloadB64)
}

// --- IsExpired ---

func TestIsExpired_ValidToken(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).Unix()
	token := makeJWT(map[string]any{"exp": future})

	expired, err := IsExpired(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Error("token with future exp should not be expired")
	}
}

func TestIsExpired_ExpiredToken(t *testing.T) {
	past := time.Now().Add(-10 * time.Minute).Unix()
	token := makeJWT(map[string]any{"exp": past})

	expired, err := IsExpired(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !expired {
		t.Error("token with past exp should be expired")
	}
}

func TestIsExpired_WithinBuffer(t *testing.T) {
	// Expires in 30 seconds - within the 60s buffer, so should be considered expired
	soon := time.Now().Add(30 * time.Second).Unix()
	token := makeJWT(map[string]any{"exp": soon})

	expired, err := IsExpired(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !expired {
		t.Error("token expiring within 60s buffer should be considered expired")
	}
}

func TestIsExpired_InvalidJWT(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"one part", "header"},
		{"two parts", "header.payload"},
		{"bad base64", "header.!!!invalid!!!.sig"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := IsExpired(tt.token)
			if err == nil {
				t.Error("expected error for invalid JWT")
			}
		})
	}
}

func TestIsExpired_NoExpClaim(t *testing.T) {
	token := makeJWT(map[string]any{"sub": "user"})
	_, err := IsExpired(token)
	if err == nil {
		t.Error("expected error for token without exp claim")
	}
}

// --- GetTokenExpiry ---

func TestGetTokenExpiry_ReturnsCorrectTime(t *testing.T) {
	ts := time.Date(2026, 3, 18, 20, 0, 0, 0, time.UTC).Unix()
	token := makeJWT(map[string]any{"exp": ts})

	expiry, err := GetTokenExpiry(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if expiry.Unix() != ts {
		t.Errorf("expiry = %v, want Unix %d", expiry, ts)
	}
}

// --- Load / Save ---

func TestLoadSave_RoundTrip(t *testing.T) {
	// Create a temp dir to act as home
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, tokenDir)

	original := &Tokens{
		Skype:       "skype-token-value",
		ChatSvcAgg:  "chatsvc-token-value",
		Teams:       "teams-token-value",
		Graph:       "graph-token-value",
		Assignments: "assignments-token-value",
	}

	// Save tokens
	if err := os.MkdirAll(tokenPath, 0700); err != nil {
		t.Fatalf("creating dir: %v", err)
	}

	_ = writeTokenFile(filepath.Join(tokenPath, skypeTokenFile), original.Skype)
	_ = writeTokenFile(filepath.Join(tokenPath, chatsvcTokenFile), original.ChatSvcAgg)
	_ = writeTokenFile(filepath.Join(tokenPath, teamsTokenFile), original.Teams)
	_ = writeTokenFile(filepath.Join(tokenPath, graphTokenFile), original.Graph)
	_ = writeTokenFile(filepath.Join(tokenPath, assignmentsTokenFile), original.Assignments)

	// Read them back
	skype, _ := readTokenFile(filepath.Join(tokenPath, skypeTokenFile))
	chatsvc, _ := readTokenFile(filepath.Join(tokenPath, chatsvcTokenFile))
	teams, _ := readTokenFile(filepath.Join(tokenPath, teamsTokenFile))
	graph, _ := readTokenFile(filepath.Join(tokenPath, graphTokenFile))
	assignments, _ := readTokenFile(filepath.Join(tokenPath, assignmentsTokenFile))

	if skype != original.Skype {
		t.Errorf("skype = %q, want %q", skype, original.Skype)
	}
	if chatsvc != original.ChatSvcAgg {
		t.Errorf("chatsvc = %q, want %q", chatsvc, original.ChatSvcAgg)
	}
	if teams != original.Teams {
		t.Errorf("teams = %q, want %q", teams, original.Teams)
	}
	if graph != original.Graph {
		t.Errorf("graph = %q, want %q", graph, original.Graph)
	}
	if assignments != original.Assignments {
		t.Errorf("assignments = %q, want %q", assignments, original.Assignments)
	}
}

func TestWriteTokenFile_Permissions(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-token.jwt")

	if err := writeTokenFile(path, "secret-token"); err != nil {
		t.Fatalf("writeTokenFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestReadTokenFile_TrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "token.jwt")

	os.WriteFile(path, []byte("  token-value  \n"), 0600)

	got, err := readTokenFile(path)
	if err != nil {
		t.Fatalf("readTokenFile: %v", err)
	}

	if got != "token-value" {
		t.Errorf("got %q, want %q", got, "token-value")
	}
}

func TestReadTokenFile_Missing(t *testing.T) {
	_, err := readTokenFile("/nonexistent/path/token.jwt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
