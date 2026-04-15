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

// newAttachmentFixture wires msgBaseURL and graphBaseURL to the same server so
// the full upload -> share link -> send flow can be routed by path.
func newAttachmentFixture(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	origMsg := msgBaseURL
	origGraph := graphBaseURL
	msgBaseURL = srv.URL
	graphBaseURL = srv.URL
	t.Cleanup(func() {
		msgBaseURL = origMsg
		graphBaseURL = origGraph
	})

	return &Client{
		http: srv.Client(),
		tokens: &auth.Tokens{
			Skype:      makeTestJWT(map[string]any{"oid": "test-user-id", "exp": 9999999999}),
			ChatSvcAgg: "test-chatsvc-token",
			Graph:      "test-graph-token",
		},
		skypeToken: "test-derived-skypetoken",
	}, srv
}

// --- UploadToOneDrive ---

func TestUploadToOneDrive_PutsBytesWithBearerToken(t *testing.T) {
	var gotBody []byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-graph-token" {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("content-type = %q", got)
		}
		if !strings.Contains(r.URL.Path, "/me/drive/root:/Microsoft Teams Chat Files/") {
			t.Errorf("path = %q", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = b
		_, _ = io.WriteString(w, `{"id":"ITEM-1","name":"file.txt","webUrl":"https://tenant-my.sharepoint.com/personal/user/Docs/file.txt"}`)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	client := &Client{
		http:   srv.Client(),
		tokens: &auth.Tokens{Graph: "test-graph-token"},
	}

	item, err := client.UploadToOneDrive(context.Background(), "file name.txt", []byte("hello-bytes"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(gotBody) != "hello-bytes" {
		t.Errorf("uploaded bytes = %q, want hello-bytes", string(gotBody))
	}
	if item.ID != "ITEM-1" {
		t.Errorf("id = %q", item.ID)
	}
}

func TestUploadToOneDrive_RetriesOn423ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			// Return 423 twice - retry layer should recover.
			w.WriteHeader(http.StatusLocked)
			return
		}
		_, _ = io.WriteString(w, `{"id":"ITEM-OK","name":"f.txt"}`)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	client := &Client{
		http:   srv.Client(),
		tokens: &auth.Tokens{Graph: "test-graph-token"},
	}

	item, err := client.UploadToOneDrive(context.Background(), "f.txt", []byte("x"))
	if err != nil {
		t.Fatalf("unexpected error after 423 retry: %v", err)
	}
	if item.ID != "ITEM-OK" {
		t.Errorf("id = %q", item.ID)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (2 locked + 1 success)", got)
	}
}

func TestUploadToOneDrive_ErrorWrapped(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	client := &Client{
		http:   srv.Client(),
		tokens: &auth.Tokens{Graph: "test-graph-token"},
	}

	_, err := client.UploadToOneDrive(context.Background(), "f.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if !strings.Contains(err.Error(), "uploading to OneDrive") {
		t.Errorf("error = %v, want wrap 'uploading to OneDrive'", err)
	}
}

// --- SendMessageWithFile ---

// routeAttachment dispatches a single httptest request to upload / share-link / send
// based on path prefix. Using strings.HasPrefix avoids ServeMux's parsing of spaces
// and colons in the OneDrive path.
func routeAttachment(upload, shareLink, send http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/me/drive/root:/"):
			upload(w, r)
		case strings.Contains(r.URL.Path, "/me/drive/items/") && strings.HasSuffix(r.URL.Path, "/createLink"):
			shareLink(w, r)
		case strings.HasPrefix(r.URL.Path, "/users/ME/conversations/"):
			send(w, r)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}
}

func TestSendMessageWithFile_FullFlow(t *testing.T) {
	var (
		sawUpload    atomic.Bool
		sawShareLink atomic.Bool
		sawSend      atomic.Bool
	)

	handler := routeAttachment(
		func(w http.ResponseWriter, r *http.Request) {
			sawUpload.Store(true)
			if r.Method != http.MethodPut {
				t.Errorf("upload method = %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-graph-token" {
				t.Errorf("upload auth = %q", got)
			}
			_, _ = io.WriteString(w, `{
				"id":"DRIVE-ITEM-ID",
				"name":"report.pdf",
				"webUrl":"https://tenant-my.sharepoint.com/personal/u_tenant/Documents/Microsoft%20Teams%20Chat%20Files/report.pdf",
				"parentReference":{"driveId":"d","path":"/drive/root:/Microsoft Teams Chat Files"},
				"sharepointIds":{"listId":"LIST","listItemUniqueId":"UNIQ","siteId":"SITE","siteUrl":"https://tenant.sharepoint.com/personal/u_tenant","webId":"WEB"}
			}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			sawShareLink.Store(true)
			if r.Method != http.MethodPost {
				t.Errorf("share method = %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-graph-token" {
				t.Errorf("share auth = %q", got)
			}
			if !strings.Contains(r.URL.Path, "/DRIVE-ITEM-ID/") {
				t.Errorf("share path = %q, want contain /DRIVE-ITEM-ID/", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			if payload["type"] != "view" {
				t.Errorf("share type = %v", payload["type"])
			}
			if payload["scope"] != "organization" {
				t.Errorf("share scope = %v", payload["scope"])
			}
			_, _ = io.WriteString(w, `{"id":"SHARE-ID","link":{"webUrl":"https://tenant.sharepoint.com/sharing/abc"}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			sawSend.Store(true)
			if r.Method != http.MethodPost {
				t.Errorf("send method = %s", r.Method)
			}
			if got := r.Header.Get("Authentication"); got != "skypetoken=test-derived-skypetoken" {
				t.Errorf("send auth = %q", got)
			}
			body, _ := io.ReadAll(r.Body)
			var msg map[string]any
			if err := json.Unmarshal(body, &msg); err != nil {
				t.Fatalf("send body not JSON: %v", err)
			}
			if msg["messagetype"] != "RichText/Html" {
				t.Errorf("messagetype = %v", msg["messagetype"])
			}
			props, _ := msg["properties"].(map[string]any)
			filesJSON, _ := props["files"].(string)
			if filesJSON == "" {
				t.Fatal("properties.files missing (should be JSON string, not object)")
			}
			var files []map[string]any
			if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
				t.Fatalf("files not a JSON string array: %v", err)
			}
			if len(files) != 1 {
				t.Fatalf("files len = %d, want 1", len(files))
			}
			entry := files[0]
			if entry["fileName"] != "report.pdf" {
				t.Errorf("fileName = %v", entry["fileName"])
			}
			if entry["fileType"] != "pdf" {
				t.Errorf("fileType = %v", entry["fileType"])
			}
			if entry["id"] != "DRIVE-ITEM-ID" {
				t.Errorf("id = %v", entry["id"])
			}
			info, _ := entry["fileInfo"].(map[string]any)
			if info["shareUrl"] != "https://tenant.sharepoint.com/sharing/abc" {
				t.Errorf("shareUrl = %v", info["shareUrl"])
			}
			if info["shareId"] != "SHARE-ID" {
				t.Errorf("shareId = %v", info["shareId"])
			}

			w.WriteHeader(http.StatusCreated)
		},
	)

	client, _ := newAttachmentFixture(t, handler)

	err := client.SendMessageWithFile(context.Background(), "19:chat@thread.v2", "here's the file", "/tmp/report.pdf", []byte("PDF-BYTES"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sawUpload.Load() {
		t.Error("upload endpoint never hit")
	}
	if !sawShareLink.Load() {
		t.Error("share link endpoint never hit")
	}
	if !sawSend.Load() {
		t.Error("send endpoint never hit")
	}
}

func TestSendMessageWithFile_Retries423OnUpload(t *testing.T) {
	var uploadCalls atomic.Int32

	handler := routeAttachment(
		func(w http.ResponseWriter, r *http.Request) {
			n := uploadCalls.Add(1)
			if n < 3 {
				w.WriteHeader(http.StatusLocked)
				return
			}
			_, _ = io.WriteString(w, `{"id":"ITEM-2","name":"x.txt","webUrl":"https://tenant-my.sharepoint.com/personal/u/Documents/x.txt"}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"SH","link":{"webUrl":"https://share"}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	)

	client, _ := newAttachmentFixture(t, handler)

	err := client.SendMessageWithFile(context.Background(), "19:chat@thread.v2", "note", "x.txt", []byte("y"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := uploadCalls.Load(); got != 3 {
		t.Errorf("upload calls = %d, want 3 (2 x 423 + success)", got)
	}
}

func TestSendMessageWithFile_UploadFailureWrapped(t *testing.T) {
	handler := routeAttachment(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("share link should not be called after upload failure")
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("send should not be called after upload failure")
		},
	)

	client, _ := newAttachmentFixture(t, handler)

	err := client.SendMessageWithFile(context.Background(), "19:chat@thread.v2", "m", "a.txt", []byte("b"))
	if err == nil {
		t.Fatal("expected error")
	}
	// Outer wrap from SendMessageWithFile plus inner wrap from UploadToOneDrive.
	if !strings.Contains(err.Error(), "uploading to OneDrive") {
		t.Errorf("error = %v, want wrap 'uploading to OneDrive'", err)
	}
}

func TestSendMessageWithFile_ShareLinkFailureWrapped(t *testing.T) {
	handler := routeAttachment(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"ITEM-3","name":"a.txt","webUrl":"https://tenant-my.sharepoint.com/personal/u/Documents/a.txt"}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			// Non-retryable status so the test stays fast.
			w.WriteHeader(http.StatusBadRequest)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("send should not be called after share link failure")
		},
	)

	client, _ := newAttachmentFixture(t, handler)

	err := client.SendMessageWithFile(context.Background(), "19:chat@thread.v2", "m", "a.txt", []byte("b"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "creating share link") {
		t.Errorf("error = %v, want wrap 'creating share link'", err)
	}
}
