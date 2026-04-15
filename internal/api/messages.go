package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GetMessages fetches messages from a conversation (channel or chat) by its ID.
// Uses the derived skypetoken for authentication.
// NOTE: The messages endpoint uses "Authentication: skypetoken=<value>" header,
// NOT "Authorization: Bearer <value>".
func (c *Client) GetMessages(ctx context.Context, conversationID string, pageSize int) ([]ChatMessage, error) {
	if err := c.EnsureSkypeToken(); err != nil {
		return nil, err
	}

	if pageSize <= 0 {
		pageSize = 200
	}

	encoded := url.PathEscape(conversationID)
	endpoint := fmt.Sprintf("%s/users/ME/conversations/%s/messages?view=msnp24Equivalent|supportsMessageProperties&pageSize=%d&startTime=1",
		msgBaseURL, encoded, pageSize)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating messages request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Accept", "application/json")

	var resp MessagesResponse
	if err := c.doRequestJSON(ctx, req, &resp); err != nil {
		return nil, fmt.Errorf("fetching messages for %s: %w", conversationID, err)
	}

	return resp.Messages, nil
}

// GetMessagesPage fetches a single page of messages and returns both messages and metadata.
// Used for pagination support.
func (c *Client) GetMessagesPage(ctx context.Context, conversationID string, pageSize int) ([]ChatMessage, *MessageMetadata, error) {
	if err := c.EnsureSkypeToken(); err != nil {
		return nil, nil, err
	}

	if pageSize <= 0 {
		pageSize = 200
	}

	encoded := url.PathEscape(conversationID)
	endpoint := fmt.Sprintf("%s/users/ME/conversations/%s/messages?view=msnp24Equivalent|supportsMessageProperties&pageSize=%d&startTime=1",
		msgBaseURL, encoded, pageSize)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating messages request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Accept", "application/json")

	var resp MessagesResponse
	if err := c.doRequestJSON(ctx, req, &resp); err != nil {
		return nil, nil, fmt.Errorf("fetching messages for %s: %w", conversationID, err)
	}

	return resp.Messages, resp.Metadata, nil
}

// GetMessagesFromURL fetches messages from a direct URL (used for pagination via backwardLink).
// The URL is validated to ensure credentials are not sent to untrusted hosts.
func (c *Client) GetMessagesFromURL(ctx context.Context, pageURL string) ([]ChatMessage, *MessageMetadata, error) {
	if err := c.EnsureSkypeToken(); err != nil {
		return nil, nil, err
	}

	if !strings.HasPrefix(pageURL, msgBaseURL) {
		return nil, nil, fmt.Errorf("refusing to send credentials to untrusted URL: %s", pageURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating messages request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Accept", "application/json")

	var resp MessagesResponse
	if err := c.doRequestJSON(ctx, req, &resp); err != nil {
		return nil, nil, fmt.Errorf("fetching messages page: %w", err)
	}

	return resp.Messages, resp.Metadata, nil
}

// GetAllMessages fetches all messages from a conversation by following backwardLink pagination.
// maxPages limits the number of pages fetched (0 = unlimited).
func (c *Client) GetAllMessages(ctx context.Context, conversationID string, pageSize int, maxPages int) ([]ChatMessage, error) {
	messages, metadata, err := c.GetMessagesPage(ctx, conversationID, pageSize)
	if err != nil {
		return nil, err
	}

	allMessages := make([]ChatMessage, 0, len(messages))
	allMessages = append(allMessages, messages...)

	page := 1
	for metadata != nil && metadata.BackwardLink != "" {
		if maxPages > 0 && page >= maxPages {
			break
		}
		if err := ctx.Err(); err != nil {
			return allMessages, err
		}

		msgs, meta, err := c.GetMessagesFromURL(ctx, metadata.BackwardLink)
		if err != nil {
			break // return what we have so far
		}

		allMessages = append(allMessages, msgs...)
		metadata = meta
		page++
	}

	return allMessages, nil
}

// MarkConversationRead marks a conversation as read by updating the consumption horizon.
func (c *Client) MarkConversationRead(ctx context.Context, conversationID string) error {
	if err := c.EnsureSkypeToken(); err != nil {
		return err
	}

	encoded := url.PathEscape(conversationID)

	now := time.Now().UnixMilli()
	payload := map[string]string{
		"consumptionhorizon": fmt.Sprintf("%d;%d;%d", now, now, now),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling mark-read body: %w", err)
	}

	endpoint := fmt.Sprintf("%s/users/ME/conversations/%s/properties?name=consumptionhorizon",
		msgBaseURL, encoded)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating mark-read request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Content-Type", "application/json")

	if _, err := c.doRequest(ctx, req); err != nil {
		return fmt.Errorf("marking conversation read: %w", err)
	}

	return nil
}

// StartNewDM creates a new 1:1 conversation with a user by their MRI or object ID.
// Returns the conversation ID for the new DM.
func (c *Client) StartNewDM(ctx context.Context, userMRI string) (string, error) {
	if err := c.EnsureSkypeToken(); err != nil {
		return "", err
	}

	if !strings.HasPrefix(userMRI, "8:orgid:") {
		userMRI = "8:orgid:" + userMRI
	}

	selfOID, err := c.getUserObjectID()
	if err != nil {
		return "", fmt.Errorf("getting self object ID for DM: %w", err)
	}
	selfMRI := "8:orgid:" + selfOID

	type member struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	type dmRequest struct {
		Members    []member          `json:"members"`
		Properties map[string]string `json:"properties"`
	}

	payload := dmRequest{
		Members: []member{
			{ID: selfMRI, Role: "User"},
			{ID: userMRI, Role: "User"},
		},
		Properties: map[string]string{
			"threadType":         "chat",
			"chatFilesIndexId":   "2",
			"fixedRoster":        "true",
			"uniquerosterthread": "true",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling DM request body: %w", err)
	}

	endpoint := fmt.Sprintf("%s/users/ME/conversations", msgBaseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating new DM request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Content-Type", "application/json")

	respHeaders, respBody, err := c.doRawRequest(ctx, req)
	if err != nil {
		return "", fmt.Errorf("creating new DM: %w", err)
	}

	if location := respHeaders.Get("Location"); location != "" {
		parts := strings.Split(location, "/conversations/")
		if len(parts) > 1 {
			return parts[1], nil
		}
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.ID != "" {
		return result.ID, nil
	}

	return "", fmt.Errorf("could not extract conversation ID from response")
}

// SendMessage sends a message to a conversation via the internal API.
func (c *Client) SendMessage(ctx context.Context, conversationID string, content string) error {
	if err := c.EnsureSkypeToken(); err != nil {
		return err
	}

	encoded := url.PathEscape(conversationID)
	endpoint := fmt.Sprintf("%s/users/ME/conversations/%s/messages",
		msgBaseURL, encoded)

	payload := struct {
		Content         string `json:"content"`
		MessageType     string `json:"messagetype"`
		ContentType     string `json:"contenttype"`
		ClientMessageID string `json:"clientmessageid"`
	}{
		Content:         content,
		MessageType:     "RichText/Html",
		ContentType:     "text",
		ClientMessageID: fmt.Sprintf("%d", time.Now().UnixMilli()),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling message body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating send request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Content-Type", "application/json")

	if _, err := c.doRequest(ctx, req); err != nil {
		return fmt.Errorf("sending message: %w", err)
	}

	return nil
}
