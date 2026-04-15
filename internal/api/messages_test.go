package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alxxpersonal/exo-teams/internal/auth"
)

// withMsgBaseURL swaps msgBaseURL for the test server URL and returns a restorer.
func withMsgBaseURL(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := msgBaseURL
	msgBaseURL = srv.URL
	t.Cleanup(func() { msgBaseURL = orig })
}

// --- GetMessages ---

func TestGetMessages(t *testing.T) {
	conversationID := "19:chat@thread.v2"

	tests := []struct {
		name       string
		pageSize   int
		wantSize   string
		respBody   string
		wantCount  int
		wantFirstID string
	}{
		{
			name:       "basic fetch with pageSize",
			pageSize:   50,
			wantSize:   "50",
			respBody:   `{"messages":[{"id":"m1"},{"id":"m2"}]}`,
			wantCount:  2,
			wantFirstID: "m1",
		},
		{
			name:      "empty response",
			pageSize:  10,
			wantSize:  "10",
			respBody:  `{"messages":[]}`,
			wantCount: 0,
		},
		{
			name:       "zero pageSize defaults to 200",
			pageSize:   0,
			wantSize:   "200",
			respBody:   `{"messages":[{"id":"only"}]}`,
			wantCount:  1,
			wantFirstID: "only",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", r.Method)
				}
				if got := r.Header.Get("Authentication"); got != "skypetoken=test-derived-skypetoken" {
					t.Errorf("auth header = %q", got)
				}
				if got := r.Header.Get("Accept"); got != "application/json" {
					t.Errorf("accept = %q", got)
				}

				// URL params
				if got := r.URL.Query().Get("pageSize"); got != tc.wantSize {
					t.Errorf("pageSize = %q, want %q", got, tc.wantSize)
				}
				if got := r.URL.Query().Get("startTime"); got != "1" {
					t.Errorf("startTime = %q, want 1", got)
				}
				// view param contains pipe, survived encoding
				if view := r.URL.Query().Get("view"); view != "msnp24Equivalent|supportsMessageProperties" {
					t.Errorf("view = %q", view)
				}
				// Conversation ID with colon must be escaped in path; server decodes it back.
				if !strings.Contains(r.URL.Path, conversationID) {
					t.Errorf("path = %q, want to contain %q", r.URL.Path, conversationID)
				}

				_, _ = io.WriteString(w, tc.respBody)
			})

			client, srv := newTestClient(handler)
			defer srv.Close()
			withMsgBaseURL(t, srv)

			msgs, err := client.GetMessages(context.Background(), conversationID, tc.pageSize)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(msgs) != tc.wantCount {
				t.Fatalf("got %d messages, want %d", len(msgs), tc.wantCount)
			}
			if tc.wantFirstID != "" && msgs[0].ID != tc.wantFirstID {
				t.Errorf("first id = %q, want %q", msgs[0].ID, tc.wantFirstID)
			}
		})
	}
}

// --- GetMessagesPage ---

func TestGetMessagesPage_ReturnsMetadata(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[{"id":"m1"}],"_metadata":{"backwardLink":"https://next.page","syncState":"s1"}}`)
	})

	client, srv := newTestClient(handler)
	defer srv.Close()
	withMsgBaseURL(t, srv)

	msgs, meta, err := client.GetMessagesPage(context.Background(), "19:chat@thread.v2", 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if meta == nil {
		t.Fatal("metadata is nil")
	}
	if meta.BackwardLink != "https://next.page" {
		t.Errorf("backwardLink = %q", meta.BackwardLink)
	}
	if meta.SyncState != "s1" {
		t.Errorf("syncState = %q", meta.SyncState)
	}
}

// --- GetAllMessages ---

func TestGetAllMessages(t *testing.T) {
	t.Run("follows backwardLink across pages", func(t *testing.T) {
		var calls atomic.Int32
		var mux http.ServeMux
		handler := &mux

		// We serve the first page at /users/ME/conversations/{id}/messages
		// and later pages at /page2 (as pointed to by backwardLink).
		var srv *httptest.Server
		mux.HandleFunc("/users/ME/conversations/", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			link := srv.URL + "/page2"
			resp := map[string]any{
				"messages":  []map[string]any{{"id": "p1-a"}, {"id": "p1-b"}},
				"_metadata": map[string]any{"backwardLink": link, "syncState": "s1"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		})
		mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			// Final page: empty backwardLink stops pagination.
			_, _ = io.WriteString(w, `{"messages":[{"id":"p2-a"}],"_metadata":{"backwardLink":"","syncState":"s2"}}`)
		})

		srv = httptest.NewServer(handler)
		defer srv.Close()
		withMsgBaseURL(t, srv)

		client := &Client{
			http: srv.Client(),
			tokens: &auth.Tokens{
				Skype:      makeTestJWT(map[string]any{"oid": "u", "exp": 9999999999}),
				ChatSvcAgg: "test-chatsvc-token",
				Graph:      "test-graph-token",
			},
			skypeToken: "test-derived-skypetoken",
		}

		msgs, err := client.GetAllMessages(context.Background(), "19:chat@thread.v2", 50, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(msgs) != 3 {
			t.Errorf("got %d messages, want 3", len(msgs))
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("calls = %d, want 2", got)
		}
	})

	t.Run("respects maxPages cap", func(t *testing.T) {
		var calls atomic.Int32
		var srv *httptest.Server
		mux := http.NewServeMux()

		// Every response advertises a further link to simulate unbounded pagination.
		mux.HandleFunc("/users/ME/conversations/", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			link := srv.URL + "/next"
			resp := map[string]any{
				"messages":  []map[string]any{{"id": "first"}},
				"_metadata": map[string]any{"backwardLink": link},
			}
			_ = json.NewEncoder(w).Encode(resp)
		})
		mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			link := srv.URL + "/next"
			resp := map[string]any{
				"messages":  []map[string]any{{"id": "again"}},
				"_metadata": map[string]any{"backwardLink": link},
			}
			_ = json.NewEncoder(w).Encode(resp)
		})

		srv = httptest.NewServer(mux)
		defer srv.Close()
		withMsgBaseURL(t, srv)

		client := &Client{
			http:       srv.Client(),
			tokens:     &auth.Tokens{Skype: makeTestJWT(map[string]any{"oid": "u", "exp": 9999999999})},
			skypeToken: "test-derived-skypetoken",
		}

		msgs, err := client.GetAllMessages(context.Background(), "19:chat@thread.v2", 25, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("calls = %d, want 2 (maxPages cap)", got)
		}
		if len(msgs) != 2 {
			t.Errorf("got %d messages, want 2", len(msgs))
		}
	})
}

// --- SendMessage ---

func TestSendMessage_PostsCorrectBodyAndHeaders(t *testing.T) {
	conversationID := "19:chat@thread.v2"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		// Skypetoken auth, not Bearer.
		if got := r.Header.Get("Authentication"); got != "skypetoken=test-derived-skypetoken" {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization header: %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		// Path should end with /messages for the encoded conversation id.
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("path = %q, want suffix /messages", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, conversationID) {
			t.Errorf("path = %q, want contains %q", r.URL.Path, conversationID)
		}

		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if msg["content"] != "hi <b>bro</b>" {
			t.Errorf("content = %v", msg["content"])
		}
		if msg["messagetype"] != "RichText/Html" {
			t.Errorf("messagetype = %v", msg["messagetype"])
		}
		if msg["contenttype"] != "text" {
			t.Errorf("contenttype = %v", msg["contenttype"])
		}
		if _, ok := msg["clientmessageid"].(string); !ok {
			t.Errorf("clientmessageid missing or not string: %v", msg["clientmessageid"])
		}

		w.WriteHeader(http.StatusCreated)
	})

	client, srv := newTestClient(handler)
	defer srv.Close()
	withMsgBaseURL(t, srv)

	if err := client.SendMessage(context.Background(), conversationID, "hi <b>bro</b>"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMessage_ServerError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	client, srv := newTestClient(handler)
	defer srv.Close()
	withMsgBaseURL(t, srv)

	err := client.SendMessage(context.Background(), "19:chat@thread.v2", "hi")
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if !strings.Contains(err.Error(), "sending message") {
		t.Errorf("error = %v, want wrapped 'sending message'", err)
	}
}

// --- StartNewDM ---

func TestStartNewDM_PostsMembersAndReturnsID(t *testing.T) {
	tests := []struct {
		name         string
		locationHdr  string
		respBody     string
		wantID       string
		wantErr      bool
	}{
		{
			name:        "resolves id from Location header",
			locationHdr: "/v1/users/ME/conversations/19:new-dm@thread.v2",
			respBody:    ``,
			wantID:      "19:new-dm@thread.v2",
		},
		{
			name:     "resolves id from body when header missing",
			respBody: `{"id":"19:fallback@thread.v2"}`,
			wantID:   "19:fallback@thread.v2",
		},
		{
			name:     "error when no id can be extracted",
			respBody: `{}`,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/users/ME/conversations" {
					t.Errorf("path = %q, want /users/ME/conversations", r.URL.Path)
				}
				if got := r.Header.Get("Authentication"); got != "skypetoken=test-derived-skypetoken" {
					t.Errorf("auth = %q", got)
				}

				body, _ := io.ReadAll(r.Body)
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("body not JSON: %v", err)
				}
				members, ok := payload["members"].([]any)
				if !ok || len(members) != 2 {
					t.Fatalf("members = %v, want 2 entries", payload["members"])
				}
				// Self MRI derived from oid claim ("test-user-id" in makeTestJWT helper).
				first, _ := members[0].(map[string]any)
				if first["id"] != "8:orgid:test-user-id" {
					t.Errorf("self MRI = %v", first["id"])
				}
				if first["role"] != "User" {
					t.Errorf("self role = %v", first["role"])
				}
				second, _ := members[1].(map[string]any)
				if second["id"] != "8:orgid:target-user" {
					t.Errorf("target MRI = %v", second["id"])
				}

				props, _ := payload["properties"].(map[string]any)
				if props["threadType"] != "chat" {
					t.Errorf("threadType = %v", props["threadType"])
				}

				if tc.locationHdr != "" {
					w.Header().Set("Location", tc.locationHdr)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, tc.respBody)
			})

			client, srv := newTestClient(handler)
			defer srv.Close()
			withMsgBaseURL(t, srv)

			id, err := client.StartNewDM(context.Background(), "target-user")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
		})
	}
}

// --- MarkConversationRead ---

func TestMarkConversationRead(t *testing.T) {
	conversationID := "19:chat@thread.v2"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/properties") {
			t.Errorf("path = %q, want suffix /properties", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "consumptionhorizon" {
			t.Errorf("name query = %q, want consumptionhorizon", got)
		}
		if got := r.Header.Get("Authentication"); got != "skypetoken=test-derived-skypetoken" {
			t.Errorf("auth = %q", got)
		}

		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		horizon := payload["consumptionhorizon"]
		if horizon == "" || strings.Count(horizon, ";") != 2 {
			t.Errorf("consumptionhorizon = %q, want three ;-separated values", horizon)
		}

		w.WriteHeader(http.StatusOK)
	})

	client, srv := newTestClient(handler)
	defer srv.Close()
	withMsgBaseURL(t, srv)

	if err := client.MarkConversationRead(context.Background(), conversationID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
