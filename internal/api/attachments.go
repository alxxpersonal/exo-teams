package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// UploadToOneDrive uploads a file to the "Microsoft Teams Chat Files" folder in OneDrive.
// If the file is locked (423), retries with a deduplicated name.
func (c *Client) UploadToOneDrive(filename string, fileData []byte) (*DriveItem, error) {
	ext := filepath.Ext(filename)
	base := filename[:len(filename)-len(ext)]

	for attempt := 0; attempt < 5; attempt++ {
		name := filename
		if attempt > 0 {
			name = fmt.Sprintf("%s (%d)%s", base, attempt, ext)
		}

		escapedName := url.PathEscape(name)
		uploadURL := fmt.Sprintf("%s/me/drive/root:/Microsoft Teams Chat Files/%s:/content", graphBaseURL, escapedName)

		req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(fileData))
		if err != nil {
			return nil, fmt.Errorf("creating upload request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.tokens.Graph)
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("uploading to OneDrive: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 423 {
			continue
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			return nil, fmt.Errorf("OneDrive upload returned %d: %s", resp.StatusCode, string(body))
		}

		var item DriveItem
		if err := json.Unmarshal(body, &item); err != nil {
			return nil, fmt.Errorf("parsing upload response: %w", err)
		}

		return &item, nil
	}

	return nil, fmt.Errorf("file locked after 5 attempts, try again later")
}

// createShareLink creates an organization-scoped sharing link for a OneDrive item.
func (c *Client) createShareLink(driveItemID string) (shareURL string, shareID string, err error) {
	linkBody := map[string]any{
		"type":  "view",
		"scope": "organization",
	}

	bodyBytes, _ := json.Marshal(linkBody)

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/me/drive/items/%s/createLink", graphBaseURL, driveItemID), bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", fmt.Errorf("creating share link request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.tokens.Graph)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("creating share link: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("share link returned %d: %s", resp.StatusCode, string(body))
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
func (c *Client) SendMessageWithFile(conversationID string, message string, filename string, fileData []byte) error {
	baseName := filepath.Base(filename)
	ext := filepath.Ext(baseName)
	fileType := ext
	if len(ext) > 0 {
		fileType = ext[1:]
	}

	// Step 1: Upload to OneDrive "Microsoft Teams Chat Files"
	item, err := c.UploadToOneDrive(baseName, fileData)
	if err != nil {
		return fmt.Errorf("uploading to OneDrive: %w", err)
	}

	// Step 2: Create sharing link
	shareURL, shareID, err := c.createShareLink(item.ID)
	if err != nil {
		return fmt.Errorf("creating share link: %w", err)
	}

	if err := c.EnsureSkypeToken(); err != nil {
		return err
	}

	// Build siteUrl and fileUrl from the webUrl
	fileURL := item.WebURL
	// siteUrl is the OneDrive personal site root
	siteURL := ""
	if item.ParentReference != nil {
		// Extract site URL from the webUrl (up to and including the user folder path)
		path := item.WebURL
		idx := 0
		for _, marker := range []string{"/personal/", "/sites/"} {
			pos := strings.Index(path, marker)
			if pos >= 0 {
				// find the next slash after the personal/username part
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

	// Build file metadata matching real Teams format
	fileEntry := map[string]any{
		"@type":    "http://schema.skype.com/File",
		"version":  2,
		"id":       itemID,
		"itemid":   itemID,
		"fileName": baseName,
		"fileType": fileType,
		"baseUrl":  siteURL,
		"objectUrl": fileURL,
		"type":     fileType,
		"title":    baseName,
		"state":    "active",
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
			"siteUrl":          item.SharePointIDs.SiteURL,
			"webId":            item.SharePointIDs.WebID,
		}
	}

	filesJSON, _ := json.Marshal([]any{fileEntry})

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

	bodyBytes, _ := json.Marshal(msgBody)

	sendURL := fmt.Sprintf("%s/users/ME/conversations/%s/messages", msgBaseURL, url.PathEscape(conversationID))
	req, err := http.NewRequest("POST", sendURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("creating send request: %w", err)
	}

	req.Header.Set("Authentication", "skypetoken="+c.skypeToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sending message with file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

