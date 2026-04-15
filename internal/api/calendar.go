package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CalendarEvent represents a calendar event.
type CalendarEvent struct {
	ID               string        `json:"id"`
	Subject          string        `json:"subject"`
	Start            *DateTimeZone `json:"start,omitempty"`
	End              *DateTimeZone `json:"end,omitempty"`
	Location         *Location     `json:"location,omitempty"`
	IsOnlineMeeting  bool          `json:"isOnlineMeeting"`
	OnlineMeetingURL string        `json:"onlineMeetingUrl"`
	BodyPreview      string        `json:"bodyPreview"`
	Organizer        *Attendee     `json:"organizer,omitempty"`
	IsAllDay         bool          `json:"isAllDay"`
	IsCancelled      bool          `json:"isCancelled"`
}

// DateTimeZone represents a date-time with its time zone.
type DateTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// Location represents a meeting location.
type Location struct {
	DisplayName string `json:"displayName"`
}

// Attendee represents a meeting attendee.
type Attendee struct {
	EmailAddress *EmailAddress `json:"emailAddress,omitempty"`
}

// EmailAddress represents an email address with display name.
type EmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// GetCalendarEvents returns upcoming calendar events.
func (g *GraphClient) GetCalendarEvents(ctx context.Context, daysAhead int) ([]CalendarEvent, error) {
	now := time.Now().UTC()
	end := now.AddDate(0, 0, daysAhead)

	startStr := now.Format("2006-01-02T15:04:05")
	endStr := end.Format("2006-01-02T15:04:05")

	endpoint := fmt.Sprintf("%s/me/calendarView?startDateTime=%s&endDateTime=%s&$orderby=start/dateTime&$top=50",
		graphBaseURL, startStr, endStr)

	body, err := g.graphRequest(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetching calendar: %w", err)
	}

	var resp graphListResponse[CalendarEvent]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing calendar: %w", err)
	}

	return resp.Value, nil
}
