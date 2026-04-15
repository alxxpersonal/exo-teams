package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// --- GetCalendarEvents ---

func TestGetCalendarEvents_BuildsCalendarViewQuery(t *testing.T) {
	var gotPath, gotStart, gotEnd, gotOrderBy, gotTop string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		q := r.URL.Query()
		gotStart = q.Get("startDateTime")
		gotEnd = q.Get("endDateTime")
		gotOrderBy = q.Get("$orderby")
		gotTop = q.Get("$top")

		_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{}})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	if _, err := gc.GetCalendarEvents(context.Background(), 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/me/calendarView" {
		t.Errorf("path = %q, want /me/calendarView", gotPath)
	}
	if gotOrderBy != "start/dateTime" {
		t.Errorf("orderby = %q", gotOrderBy)
	}
	if gotTop != "50" {
		t.Errorf("top = %q", gotTop)
	}

	// startDateTime / endDateTime are emitted as ISO 8601 without timezone suffix
	// (server treats the value as UTC). Verify both parse and that end - start == 7 days.
	layout := "2006-01-02T15:04:05"
	startT, err := time.Parse(layout, gotStart)
	if err != nil {
		t.Fatalf("start %q not ISO 8601: %v", gotStart, err)
	}
	endT, err := time.Parse(layout, gotEnd)
	if err != nil {
		t.Fatalf("end %q not ISO 8601: %v", gotEnd, err)
	}
	if delta := endT.Sub(startT); delta < 7*24*time.Hour-time.Second || delta > 7*24*time.Hour+time.Second {
		t.Errorf("end - start = %s, want ~168h", delta)
	}
}

func TestGetCalendarEvents_EmptyReturnsEmptySlice(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{}})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	events, err := gc.GetCalendarEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events len = %d, want 0", len(events))
	}
}

func TestGetCalendarEvents_ParsesRecurringShape(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"id":              "evt-1",
					"subject":         "Lecture",
					"isAllDay":        false,
					"isCancelled":     false,
					"isOnlineMeeting": true,
					"onlineMeetingUrl": "https://teams.example/meet/123",
					"bodyPreview":     "Weekly lecture",
					"start": map[string]any{
						"dateTime": "2026-04-15T09:00:00.0000000",
						"timeZone": "UTC",
					},
					"end": map[string]any{
						"dateTime": "2026-04-15T10:30:00.0000000",
						"timeZone": "UTC",
					},
					"location": map[string]any{"displayName": "Room 101"},
					"organizer": map[string]any{
						"emailAddress": map[string]any{
							"name":    "Prof Smith",
							"address": "smith@uni.example",
						},
					},
				},
				{
					"id":          "evt-2",
					"subject":     "All Day Holiday",
					"isAllDay":    true,
					"isCancelled": true,
					"start":       map[string]any{"dateTime": "2026-04-20T00:00:00.0000000", "timeZone": "UTC"},
					"end":         map[string]any{"dateTime": "2026-04-21T00:00:00.0000000", "timeZone": "UTC"},
				},
			},
		})
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	events, err := gc.GetCalendarEvents(context.Background(), 14)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}

	first := events[0]
	if first.Subject != "Lecture" {
		t.Errorf("subject = %q", first.Subject)
	}
	if !first.IsOnlineMeeting || first.OnlineMeetingURL != "https://teams.example/meet/123" {
		t.Errorf("online meeting fields wrong: %+v", first)
	}
	if first.Start == nil || first.Start.TimeZone != "UTC" {
		t.Errorf("start not parsed: %+v", first.Start)
	}
	if first.Location == nil || first.Location.DisplayName != "Room 101" {
		t.Errorf("location not parsed: %+v", first.Location)
	}
	if first.Organizer == nil || first.Organizer.EmailAddress == nil || first.Organizer.EmailAddress.Address != "smith@uni.example" {
		t.Errorf("organizer not parsed: %+v", first.Organizer)
	}

	second := events[1]
	if !second.IsAllDay || !second.IsCancelled {
		t.Errorf("all-day / cancelled flags wrong: %+v", second)
	}
}

func TestGetCalendarEvents_ErrorOnNon200(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	gc, srv := newTestGraphClient(handler)
	defer srv.Close()
	withGraphBaseURL(t, srv)

	if _, err := gc.GetCalendarEvents(context.Background(), 3); err == nil {
		t.Error("expected error on 401")
	}
}
