package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DriveItem represents a file or folder in SharePoint/OneDrive.
type DriveItem struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Size             int64  `json:"size"`
	WebURL           string `json:"webUrl"`
	LastModifiedDateTime string `json:"lastModifiedDateTime"`
	LastModifiedBy   *IdentitySet `json:"lastModifiedBy,omitempty"`
	Folder           *FolderFacet `json:"folder,omitempty"`
	File             *FileFacet   `json:"file,omitempty"`
	DownloadURL      string `json:"@microsoft.graph.downloadUrl"`
	ParentReference  *ParentReference  `json:"parentReference,omitempty"`
	SharePointIDs    *SharePointIDs    `json:"sharepointIds,omitempty"`
}

// ParentReference holds info about a drive item's parent folder.
type ParentReference struct {
	DriveID   string `json:"driveId"`
	DriveType string `json:"driveType"`
	ID        string `json:"id"`
	Path      string `json:"path"`
	SiteID    string `json:"siteId"`
}

// SharePointIDs holds SharePoint-specific identifiers for a drive item.
type SharePointIDs struct {
	ListID           string `json:"listId"`
	ListItemUniqueID string `json:"listItemUniqueId"`
	SiteID           string `json:"siteId"`
	SiteURL          string `json:"siteUrl"`
	WebID            string `json:"webId"`
}

// IdentitySet represents a set of identities associated with an action.
type IdentitySet struct {
	User *Identity `json:"user,omitempty"`
}

// Identity represents a user identity with display name and ID.
type Identity struct {
	DisplayName string `json:"displayName"`
	ID          string `json:"id"`
}

// FolderFacet indicates the item is a folder and holds child count.
type FolderFacet struct {
	ChildCount int `json:"childCount"`
}

// FileFacet indicates the item is a file and holds its MIME type.
type FileFacet struct {
	MimeType string `json:"mimeType"`
}

// TeamDrive represents a drive (document library) within a team.
type TeamDrive struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetTeamDrives returns all drives for a team.
// Education teams typically have "Documents" + "Class Materials".
func (g *GraphClient) GetTeamDrives(teamID string) ([]TeamDrive, error) {
	body, err := g.graphRequest(graphBaseURL + "/groups/" + teamID + "/drives")
	if err != nil {
		return nil, fmt.Errorf("fetching drives: %w", err)
	}

	var resp graphListResponse[TeamDrive]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing drives: %w", err)
	}

	return resp.Value, nil
}

// GetTeamAllFiles returns files from ALL drives in a team (Documents + Class Materials etc).
func (g *GraphClient) GetTeamAllFiles(teamID string) (map[string][]DriveItem, error) {
	drives, err := g.GetTeamDrives(teamID)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]DriveItem)
	for _, drive := range drives {
		files, err := g.GetDriveFiles(drive.ID)
		if err != nil {
			continue
		}
		result[drive.Name] = files
	}

	return result, nil
}

// GetDriveFiles returns files from a specific drive root.
func (g *GraphClient) GetDriveFiles(driveID string) ([]DriveItem, error) {
	body, err := g.graphRequest(graphBaseURL + "/drives/" + driveID + "/root/children")
	if err != nil {
		return nil, fmt.Errorf("fetching drive files: %w", err)
	}

	var resp graphListResponse[DriveItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing drive files: %w", err)
	}

	return resp.Value, nil
}

// GetDriveFilesByPath returns files at a path in a specific drive.
func (g *GraphClient) GetDriveFilesByPath(driveID, path string) ([]DriveItem, error) {
	body, err := g.graphRequest(graphBaseURL + "/drives/" + driveID + "/root:/" + path + ":/children")
	if err != nil {
		return nil, fmt.Errorf("fetching drive files at path: %w", err)
	}

	var resp graphListResponse[DriveItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing drive files: %w", err)
	}

	return resp.Value, nil
}

// GetTeamFiles returns files from a team's default document library.
func (g *GraphClient) GetTeamFiles(teamID string) ([]DriveItem, error) {
	body, err := g.graphRequest(graphBaseURL + "/groups/" + teamID + "/drive/root/children")
	if err != nil {
		return nil, fmt.Errorf("fetching team files: %w", err)
	}

	var resp graphListResponse[DriveItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing files: %w", err)
	}

	return resp.Value, nil
}

// GetChannelFiles returns files from a specific channel's folder.
func (g *GraphClient) GetChannelFiles(teamID, channelDisplayName string) ([]DriveItem, error) {
	// Channel files are in a subfolder named after the channel
	body, err := g.graphRequest(graphBaseURL + "/groups/" + teamID + "/drive/root:/" + channelDisplayName + ":/children")
	if err != nil {
		// Try "General" folder which maps to "Shared Documents"
		body, err = g.graphRequest(graphBaseURL + "/groups/" + teamID + "/drive/root/children")
		if err != nil {
			return nil, fmt.Errorf("fetching channel files: %w", err)
		}
	}

	var resp graphListResponse[DriveItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing files: %w", err)
	}

	return resp.Value, nil
}

// GetDriveItemChildren returns children of a specific folder.
func (g *GraphClient) GetDriveItemChildren(driveID, itemID string) ([]DriveItem, error) {
	body, err := g.graphRequest(graphBaseURL + "/drives/" + driveID + "/items/" + itemID + "/children")
	if err != nil {
		return nil, fmt.Errorf("fetching folder contents: %w", err)
	}

	var resp graphListResponse[DriveItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing folder contents: %w", err)
	}

	return resp.Value, nil
}

// GetTeamFilesByPath returns files from a team's drive at a specific path.
// path should be like "General" or "General/Subfolder".
func (g *GraphClient) GetTeamFilesByPath(teamID, path string) ([]DriveItem, error) {
	endpoint := graphBaseURL + "/groups/" + teamID + "/drive/root:/" + path + ":/children"
	body, err := g.graphRequest(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetching files at path %q: %w", path, err)
	}

	var resp graphListResponse[DriveItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing files at path: %w", err)
	}

	return resp.Value, nil
}

// GetTeamFileByPath returns a single drive item at a specific path.
// Used to get download URL for a file.
func (g *GraphClient) GetTeamFileByPath(teamID, path string) (*DriveItem, error) {
	endpoint := graphBaseURL + "/groups/" + teamID + "/drive/root:/" + path
	body, err := g.graphRequest(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetching file at path %q: %w", path, err)
	}

	var item DriveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("parsing file item: %w", err)
	}

	return &item, nil
}

// GetDriveFileByPath returns a single drive item at a specific path within a specific drive.
func (g *GraphClient) GetDriveFileByPath(driveID, path string) (*DriveItem, error) {
	endpoint := graphBaseURL + "/drives/" + driveID + "/root:/" + path
	body, err := g.graphRequest(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetching file at path %q: %w", path, err)
	}

	var item DriveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("parsing file item: %w", err)
	}

	return &item, nil
}

// UploadTeamFile uploads a file to a team's drive at the specified path.
// Uses PUT to /groups/{groupId}/drive/root:/{path}/{filename}:/content
func (g *GraphClient) UploadTeamFile(teamID, remotePath string, data []byte) (*DriveItem, error) {
	endpoint := fmt.Sprintf("%s/groups/%s/drive/root:/%s:/content", graphBaseURL, teamID, remotePath)

	req, err := http.NewRequest("PUT", endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating upload request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("uploading file: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading upload response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("upload returned status %d: %s", resp.StatusCode, string(body))
	}

	var item DriveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("parsing upload response: %w", err)
	}

	return &item, nil
}

// DownloadFile downloads content from a URL and returns the raw bytes.
func (g *GraphClient) DownloadFile(downloadURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating download request: %w", err)
	}

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading download body: %w", err)
	}

	return data, nil
}
