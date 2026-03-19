package api

import (
	"fmt"
	"net/http"
)

// ActivityMessage represents a notification from the 48:notifications thread.
type ActivityMessage struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	MessageType string `json:"messagetype"`
	ComposeTime string `json:"composetime"`
	Content     string `json:"content"`
	From        string `json:"from"`
	ImDisplayName string `json:"imdisplayname"`
	Properties  *ActivityProperties `json:"properties,omitempty"`
}

// ActivityProperties contains the activity metadata.
type ActivityProperties struct {
	Activity *ActivityDetail `json:"activity,omitempty"`
}

// ActivityDetail holds the actual activity info.
type ActivityDetail struct {
	ActivityType           string `json:"activityType"`
	ActivitySubtype        string `json:"activitySubtype"`
	ActivityTimestamp       string `json:"activityTimestamp"`
	SourceThreadID         string `json:"sourceThreadId"`
	SourceMessageID        any    `json:"sourceMessageId"`
	SourceUserImDisplayName string `json:"sourceUserImDisplayName"`
}

// ActivityResponse wraps the notifications response.
type ActivityResponse struct {
	Messages []ActivityMessage `json:"messages"`
}

// GetActivity fetches the activity feed from the 48:notifications conversation thread.
func (c *Client) GetActivity(count int) ([]ActivityMessage, error) {
	if err := c.EnsureSkypeToken(); err != nil {
		return nil, err
	}

	if count <= 0 {
		count = 30
	}

	endpoint := fmt.Sprintf("%s/users/ME/conversations/48:notifications/messages?pageSize=%d&startTime=1",
		msgBaseURL, count)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating activity request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Accept", "application/json")

	var resp ActivityResponse
	if err := c.doRequestJSON(req, &resp); err != nil {
		return nil, fmt.Errorf("fetching activity: %w", err)
	}

	return resp.Messages, nil
}
