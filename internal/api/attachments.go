package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// UploadToOneDrive uploads a file to the "Microsoft Teams Chat Files" folder in OneDrive.
// Relies on the retry layer to handle transient 423 (locked) responses.
func (c *Client) UploadToOneDrive(ctx context.Context, filename string, fileData []byte) (*DriveItem, error) {
	escapedName := url.PathEscape(filename)
	uploadURL := fmt.Sprintf("%s/me/drive/root:/Microsoft Teams Chat Files/%s:/content", graphBaseURL, escapedName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(fileData))
	if err != nil {
		return nil, fmt.Errorf("creating upload request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.tokens.Graph)
	req.Header.Set("Content-Type", "application/octet-stream")

	body, err := c.doRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("uploading to OneDrive: %w", err)
	}

	var item DriveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("parsing upload response: %w", err)
	}

	return &item, nil
}

// createShareLink creates an organization-scoped sharing link for a OneDrive item.
func (c *Client) createShareLink(ctx context.Context, driveItemID string) (shareURL string, shareID string, err error) {
	linkBody := map[string]any{
		"type":  "view",
		"scope": "organization",
	}

	bodyBytes, err := json.Marshal(linkBody)
	if err != nil {
		return "", "", fmt.Errorf("marshaling share link body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/me/drive/items/%s/createLink", graphBaseURL, driveItemID),
		bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", fmt.Errorf("creating share link request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.tokens.Graph)
	req.Header.Set("Content-Type", "application/json")

	body, err := c.doRequest(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("creating share link: %w", err)
	}

	var result struct {
		ID   string `json:"id"`
		Link struct {
			WebURL string `json:"webUrl"`
		} `json:"link"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("parsing share link response: %w", err)
	}

	return result.Link.WebURL, result.ID, nil
}

// SendMessageWithFile sends a message with a file attachment to a conversation.
// Uploads to OneDrive via Graph API, creates share link, then sends via messaging API.
func (c *Client) SendMessageWithFile(ctx context.Context, conversationID string, message string, filename string, fileData []byte) error {
	baseName := filepath.Base(filename)
	ext := filepath.Ext(baseName)
	fileType := ext
	if len(ext) > 0 {
		fileType = ext[1:]
	}

	item, err := c.UploadToOneDrive(ctx, baseName, fileData)
	if err != nil {
		return fmt.Errorf("uploading to OneDrive: %w", err)
	}

	shareURL, shareID, err := c.createShareLink(ctx, item.ID)
	if err != nil {
		return fmt.Errorf("creating share link: %w", err)
	}

	if err := c.EnsureSkypeToken(); err != nil {
		return err
	}

	fileURL := item.WebURL
	siteURL := ""
	if item.ParentReference != nil {
		path := item.WebURL
		idx := 0
		for _, marker := range []string{"/personal/", "/sites/"} {
			pos := strings.Index(path, marker)
			if pos >= 0 {
				rest := path[pos+len(marker):]
				slashPos := strings.Index(rest, "/")
				if slashPos >= 0 {
					idx = pos + len(marker) + slashPos + 1
				}
				break
			}
		}
		if idx > 0 {
			siteURL = path[:idx]
		}
	}

	itemID := item.ID

	fileEntry := map[string]any{
		"@type":     "http://schema.skype.com/File",
		"version":   2,
		"id":        itemID,
		"itemid":    itemID,
		"fileName":  baseName,
		"fileType":  fileType,
		"baseUrl":   siteURL,
		"objectUrl": fileURL,
		"type":      fileType,
		"title":     baseName,
		"state":     "active",
		"permissionScope": "users",
		"fileInfo": map[string]any{
			"itemId":            nil,
			"fileUrl":           fileURL,
			"siteUrl":           siteURL,
			"serverRelativeUrl": "",
			"shareUrl":          shareURL,
			"shareId":           shareID,
		},
		"fileChicletState": map[string]string{
			"serviceName": "p2p",
			"state":       "active",
		},
		"botFileProperties":  map[string]any{},
		"filePreview":        map[string]any{"previewUrl": "", "previewHeight": 0, "previewWidth": 0},
		"providerData":       "",
		"chicletBreadcrumbs": nil,
	}

	if item.SharePointIDs != nil {
		fileEntry["sharepointIds"] = map[string]any{
			"listId":           item.SharePointIDs.ListID,
			"listItemUniqueId": item.SharePointIDs.ListItemUniqueID,
			"siteId":           item.SharePointIDs.SiteID,
			"siteUrl":           item.SharePointIDs.SiteURL,
			"webId":            item.SharePointIDs.WebID,
		}
	}

	filesJSON, err := json.Marshal([]any{fileEntry})
	if err != nil {
		return fmt.Errorf("marshaling file metadata: %w", err)
	}

	clientMsgID := fmt.Sprintf("%d", time.Now().UnixMilli())

	msgBody := map[string]any{
		"content":         message,
		"messagetype":     "RichText/Html",
		"contenttype":     "text",
		"clientmessageid": clientMsgID,
		"properties": map[string]any{
			"files":         string(filesJSON),
			"cards":         "[]",
			"links":         "[]",
			"mentions":      "[]",
			"formatVariant": "TEAMS",
		},
	}

	bodyBytes, err := json.Marshal(msgBody)
	if err != nil {
		return fmt.Errorf("marshaling message body: %w", err)
	}

	sendURL := fmt.Sprintf("%s/users/ME/conversations/%s/messages", msgBaseURL, url.PathEscape(conversationID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("creating send request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Content-Type", "application/json")

	if _, err := c.doRequest(ctx, req); err != nil {
		return fmt.Errorf("sending message with file: %w", err)
	}

	return nil
}
