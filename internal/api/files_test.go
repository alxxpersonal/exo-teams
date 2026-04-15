package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withGraphBaseURL redirects graphBaseURL to the given httptest server for the
// duration of the test and restores the original value in a cleanup hook.
func withGraphBaseURL(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := graphBaseURL
	graphBaseURL = srv.URL
	t.Cleanup(func() { graphBaseURL = orig })
}

// --- GetTeamDrives ---

func TestGetTeamDrives_ParsesMultipleDrives(t *testing.T) {
	var gotPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{"id": "drive-1", "name": "Documents"},
				{"id": "drive-2", "name": "Class Materials"},
			},
		})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	drives, err := gc.GetTeamDrives(context.Background(), "team-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(drives) != 2 {
		t.Fatalf("got %d drives, want 2", len(drives))
	}
	if drives[0].Name != "Documents" || drives[1].Name != "Class Materials" {
		t.Errorf("names = %q,%q", drives[0].Name, drives[1].Name)
	}
	if gotPath != "/groups/team-abc/drives" {
		t.Errorf("path = %q, want /groups/team-abc/drives", gotPath)
	}
}

func TestGetTeamDrives_ErrorOnNon200(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	if _, err := gc.GetTeamDrives(context.Background(), "team-abc"); err == nil {
		t.Error("expected error on 403")
	}
}

// --- GetTeamFiles ---

func TestGetTeamFiles_HitsDefaultDriveRoot(t *testing.T) {
	var gotPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{"id": "item-1", "name": "readme.md", "size": 42},
			},
		})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	items, err := gc.GetTeamFiles(context.Background(), "team-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/groups/team-xyz/drive/root/children" {
		t.Errorf("path = %q", gotPath)
	}
	if len(items) != 1 || items[0].Name != "readme.md" || items[0].Size != 42 {
		t.Errorf("items = %+v", items)
	}
}

// --- GetDriveFiles ---

func TestGetDriveFiles_HitsDriveChildrenEndpoint(t *testing.T) {
	var gotPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"id":     "folder-1",
					"name":   "Lectures",
					"folder": map[string]any{"childCount": 3},
				},
				{
					"id":   "file-1",
					"name": "notes.pdf",
					"file": map[string]any{"mimeType": "application/pdf"},
					"@microsoft.graph.downloadUrl": "https://download.example/notes",
				},
			},
		})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	items, err := gc.GetDriveFiles(context.Background(), "drive-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/drives/drive-123/root/children" {
		t.Errorf("path = %q", gotPath)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0].Folder == nil || items[0].Folder.ChildCount != 3 {
		t.Errorf("folder facet not parsed: %+v", items[0].Folder)
	}
	if items[1].File == nil || items[1].File.MimeType != "application/pdf" {
		t.Errorf("file facet not parsed: %+v", items[1].File)
	}
	if items[1].DownloadURL != "https://download.example/notes" {
		t.Errorf("downloadUrl = %q", items[1].DownloadURL)
	}
}

// --- GetTeamFilesByPath ---

func TestGetTeamFilesByPath_EncodesSpecialChars(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{
			name:     "plain",
			path:     "General",
			wantPath: "/groups/team-1/drive/root:/General:/children",
		},
		{
			name:     "space",
			path:     "General/Lecture Notes",
			wantPath: "/groups/team-1/drive/root:/General/Lecture Notes:/children",
		},
		{
			name:     "unicode",
			path:     "Wykłady",
			wantPath: "/groups/team-1/drive/root:/Wykłady:/children",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// r.URL.Path is already percent-decoded by net/http.
				gotPath = r.URL.Path
				_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{}})
			})

			gc, srv := newTestGraphClient(handler)
			defer srv.Close()
			withGraphBaseURL(t, srv)

			if _, err := gc.GetTeamFilesByPath(context.Background(), "team-1", tc.path); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

// --- GetTeamAllFiles ---

func TestGetTeamAllFiles_AggregatesAcrossDrives(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/groups/team-7/drives":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": "drive-a", "name": "Documents"},
					{"id": "drive-b", "name": "Class Materials"},
				},
			})
		case "/drives/drive-a/root/children":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": "a1", "name": "syllabus.pdf"},
				},
			})
		case "/drives/drive-b/root/children":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": "b1", "name": "lecture1.pptx"},
					{"id": "b2", "name": "lecture2.pptx"},
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	result, err := gc.GetTeamAllFiles(context.Background(), "team-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("drive buckets = %d, want 2", len(result))
	}
	if len(result["Documents"]) != 1 || result["Documents"][0].Name != "syllabus.pdf" {
		t.Errorf("Documents bucket wrong: %+v", result["Documents"])
	}
	if len(result["Class Materials"]) != 2 {
		t.Errorf("Class Materials count = %d, want 2", len(result["Class Materials"]))
	}
}

func TestGetTeamAllFiles_SkipsFailedDrive(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/groups/team-7/drives":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": "drive-good", "name": "Documents"},
					{"id": "drive-bad", "name": "Broken"},
				},
			})
		case "/drives/drive-good/root/children":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{{"id": "g", "name": "ok.txt"}},
			})
		case "/drives/drive-bad/root/children":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	result, err := gc.GetTeamAllFiles(context.Background(), "team-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := result["Documents"]; !ok {
		t.Error("expected good drive to appear in result")
	}
	if _, ok := result["Broken"]; ok {
		t.Error("expected failed drive to be skipped")
	}
}

// --- DownloadFile ---

func TestDownloadFile_ReturnsBody_NoAuth(t *testing.T) {
	want := []byte("hello file bytes")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Download URL is unauthenticated - no bearer token should be attached.
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("Authorization should not be set on download, got %q", auth)
		}
		_, _ = w.Write(want)
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()

	got, err := gc.DownloadFile(context.Background(), srv.URL+"/share/xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestDownloadFile_NotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()

	_, err := gc.DownloadFile(context.Background(), srv.URL+"/missing")
	if err == nil {
		t.Error("expected error for 404")
	}
}

// --- UploadTeamFile ---

func TestUploadTeamFile_PutsContentAndParsesResponse(t *testing.T) {
	payload := []byte("file-payload-bytes")
	var (
		gotMethod  string
		gotPath    string
		gotCT      string
		gotAuth    string
		gotBody    []byte
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "uploaded-item-id",
			"name":   "notes.txt",
			"size":   float64(len(payload)),
			"webUrl": "https://sharepoint.example/notes.txt",
		})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	item, err := gc.UploadTeamFile(context.Background(), "team-u", "General/notes.txt", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/groups/team-u/drive/root:/General/notes.txt:/content" {
		t.Errorf("path = %q", gotPath)
	}
	if gotCT != "application/octet-stream" {
		t.Errorf("content-type = %q", gotCT)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("missing bearer auth, got %q", gotAuth)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body mismatch: got %d bytes, want %d", len(gotBody), len(payload))
	}
	if item == nil || item.ID != "uploaded-item-id" || item.Name != "notes.txt" {
		t.Errorf("parsed item = %+v", item)
	}
}

func TestUploadTeamFile_ErrorOnNon2xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("nope"))
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	if _, err := gc.UploadTeamFile(context.Background(), "t", "p/x.txt", []byte("x")); err == nil {
		t.Error("expected error on 403 upload")
	}
}
