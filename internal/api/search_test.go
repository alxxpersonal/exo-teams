package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"testing"
)

// --- SearchMessages ---

func TestSearchMessages_SendsPOSTWithCorrectBody(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotCT     string
		gotAuth   string
		gotBody   map[string]any
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{}})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	if _, err := gc.SearchMessages(context.Background(), "project alpha"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/search/query" {
		t.Errorf("path = %q, want /search/query", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotAuth != "Bearer test-graph-token" {
		t.Errorf("auth = %q", gotAuth)
	}

	// Body shape: {"requests":[{"entityTypes":["message","driveItem"],"query":{"queryString":"..."},"from":0,"size":25}]}
	requests, ok := gotBody["requests"].([]any)
	if !ok || len(requests) != 1 {
		t.Fatalf("requests = %v", gotBody["requests"])
	}
	req0, _ := requests[0].(map[string]any)

	ents, _ := req0["entityTypes"].([]any)
	gotEnts := make([]string, 0, len(ents))
	for _, e := range ents {
		gotEnts = append(gotEnts, e.(string))
	}
	sort.Strings(gotEnts)
	wantEnts := []string{"driveItem", "message"}
	if !reflect.DeepEqual(gotEnts, wantEnts) {
		t.Errorf("entityTypes = %v, want %v", gotEnts, wantEnts)
	}

	qobj, _ := req0["query"].(map[string]any)
	if qs, _ := qobj["queryString"].(string); qs != "project alpha" {
		t.Errorf("queryString = %q, want 'project alpha'", qs)
	}

	// from / size are JSON numbers (float64 after decode).
	if from, _ := req0["from"].(float64); from != 0 {
		t.Errorf("from = %v, want 0", req0["from"])
	}
	if size, _ := req0["size"].(float64); size != 25 {
		t.Errorf("size = %v, want 25", req0["size"])
	}
}

func TestSearchMessages_ParsesHitsAcrossContainers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"hits": []map[string]any{
						{"summary": "msg hit", "resource": map[string]any{"id": "m-1"}},
					},
					"total":                1,
					"moreResultsAvailable": false,
				},
				{
					"hits": []map[string]any{
						{"summary": "file hit 1", "resource": map[string]any{"id": "d-1"}},
						{"summary": "file hit 2", "resource": map[string]any{"id": "d-2"}},
					},
					"total":                2,
					"moreResultsAvailable": true,
				},
			},
		})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	hits, err := gc.SearchMessages(context.Background(), "q")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits len = %d, want 3", len(hits))
	}
	if hits[0].Summary != "msg hit" || hits[0].Resource["id"] != "m-1" {
		t.Errorf("hit[0] = %+v", hits[0])
	}
	if hits[2].Summary != "file hit 2" {
		t.Errorf("hit[2].summary = %q", hits[2].Summary)
	}
}

func TestSearchMessages_EmptyResults(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no-containers", `{"value":[]}`},
		{"empty-container", `{"value":[{"hits":[],"total":0}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			})

			gc, srv := newTestGraphClient(handler)
			defer srv.Close()
			withGraphBaseURL(t, srv)

			hits, err := gc.SearchMessages(context.Background(), "nothing")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(hits) != 0 {
				t.Errorf("hits len = %d, want 0", len(hits))
			}
		})
	}
}

func TestSearchMessages_ErrorOnNon2xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	if _, err := gc.SearchMessages(context.Background(), "q"); err == nil {
		t.Error("expected error on 400")
	}
}
