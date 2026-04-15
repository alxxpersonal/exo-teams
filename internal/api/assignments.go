package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alxxpersonal/exo-teams/internal/auth"
)

const (
	// assignmentsBaseURL is the OneNote assignments API that works without admin consent.
	// The Graph /education/ endpoints require EduAssignments.* scopes which need admin consent.
	// The assignments.onenote.com API accepts tokens scoped to https://assignments.onenote.com/.default
	// and grants Notes.ReadWrite + Notes.ReadWrite.All, which is enough for read access.
	assignmentsBaseURL = "https://assignments.onenote.com/api/v1.0/edu"
	graphBaseURL       = "https://graph.microsoft.com/v1.0"
)

// Assignment represents a Teams assignment.
type Assignment struct {
	ID                   string                  `json:"id"`
	DisplayName          string                  `json:"displayName"`
	DueDateTime          string                  `json:"dueDateTime"`
	AssignedDateTime     string                  `json:"assignedDateTime"`
	CreatedDateTime      string                  `json:"createdDateTime"`
	Status               string                  `json:"status"`
	Instructions         *AssignmentInstructions `json:"instructions,omitempty"`
	ClassID              string                  `json:"classId"`
	ClassName            string                  `json:"-"` // populated after fetch
	AllowLateSubmissions bool                    `json:"allowLateSubmissions"`
	CloseDateTime        string                  `json:"closeDateTime"`
	WebURL               string                  `json:"webUrl"`
}

// AssignmentInstructions holds the assignment description.
type AssignmentInstructions struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
}

// AssignmentSubmission represents a student's submission.
type AssignmentSubmission struct {
	ID                string               `json:"id"`
	Status            string               `json:"status"`
	SubmittedDateTime string               `json:"submittedDateTime"`
	ReturnedDateTime  string               `json:"returnedDateTime"`
	ViewedDateTime    string               `json:"viewedDateTime"`
	Resources         []SubmissionResource `json:"-"` // populated separately
}

// SubmissionResource represents a file or link attached to a submission.
type SubmissionResource struct {
	ID       string `json:"id"`
	Resource struct {
		Type        string `json:"@odata.type"`
		DisplayName string `json:"displayName"`
		Link        string `json:"link,omitempty"`
		FileURL     string `json:"fileUrl,omitempty"`
	} `json:"resource"`
}

// AssignmentWithSubmission combines assignment info with your submission status.
type AssignmentWithSubmission struct {
	Assignment
	SubmissionStatus   string               `json:"submissionStatus"`
	SubmittedDateTime  string               `json:"submittedDateTime"`
	SubmissionID       string               `json:"submissionId"`
	SubmittedResources []SubmissionResource `json:"submittedResources,omitempty"`
}

// EducationClass represents a Teams education class.
type EducationClass struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Name        string `json:"name,omitempty"` // assignments.onenote.com uses "name"
	Description string `json:"description,omitempty"`
	Role        string `json:"role,omitempty"`
	Status      string `json:"status,omitempty"`
}

// GetDisplayName returns the class display name, handling both Graph and assignments API formats.
func (c *EducationClass) GetDisplayName() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Name
}

type graphListResponse[T any] struct {
	Value []T `json:"value"`
}

// GraphClient wraps HTTP calls to Microsoft Graph API and the assignments API.
type GraphClient struct {
	token            string       // graph token (for calendar, files, search, etc.)
	assignmentsToken string       // assignments.onenote.com token
	http             *http.Client // HTTP client with timeout
}

// newHTTPClient returns an HTTP client with a 60-second timeout.
// Graph operations like search and file downloads can be slow,
// so this is intentionally longer than the 30s internal client timeout.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

// NewGraphClient creates a client for Graph API calls.
func NewGraphClient(graphToken string) *GraphClient {
	return &GraphClient{token: graphToken, http: newHTTPClient()}
}

// NewGraphClientWithAssignments creates a client with both Graph and assignments tokens.
func NewGraphClientWithAssignments(graphToken, assignmentsToken string) *GraphClient {
	return &GraphClient{token: graphToken, assignmentsToken: assignmentsToken, http: newHTTPClient()}
}

// tokenKind identifies which token a GraphClient request is authenticated with.
// Used so 401 refresh can swap in the correct refreshed token on retry.
type tokenKind int

const (
	tokenGraph tokenKind = iota
	tokenAssignments
)

// tokenFor returns the current token value for the given kind, honoring the
// graph-token fallback that is used when no dedicated assignments token exists.
func (g *GraphClient) tokenFor(kind tokenKind) string {
	if kind == tokenAssignments {
		if g.assignmentsToken != "" {
			return g.assignmentsToken
		}
	}
	return g.token
}

func (g *GraphClient) graphRequest(ctx context.Context, endpoint string) ([]byte, error) {
	return g.doAuthRequest(ctx, endpoint, tokenGraph)
}

func (g *GraphClient) assignmentsRequest(ctx context.Context, endpoint string) ([]byte, error) {
	return g.doAuthRequest(ctx, endpoint, tokenAssignments)
}

// doAuthRequest performs a GET with a bearer token, with retry, 401 auto-refresh,
// and sanitized error messages. Errors never include the response body.
func (g *GraphClient) doAuthRequest(ctx context.Context, endpoint string, kind tokenKind) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	parsed, perr := url.Parse(endpoint)
	if perr != nil {
		return nil, fmt.Errorf("parsing url: %w", perr)
	}

	build := func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+g.tokenFor(kind))
		return r, nil
	}

	resp, err := retryDo(retryDoer{ctx: ctx, client: g.http, buildReq: build})
	if err != nil {
		return nil, fmt.Errorf("executing GET %s: %w", sanitizeURL(parsed), err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if rerr := g.refreshToken(); rerr != nil {
			return nil, fmt.Errorf("GET %s returned status %d", sanitizeURL(parsed), http.StatusUnauthorized)
		}
		resp, err = retryDo(retryDoer{ctx: ctx, client: g.http, buildReq: build})
		if err != nil {
			return nil, fmt.Errorf("executing GET %s: %w", sanitizeURL(parsed), err)
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from GET %s: %w", sanitizeURL(parsed), err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned status %d", sanitizeURL(parsed), resp.StatusCode)
	}

	return body, nil
}

// doBearerRequest performs an arbitrary HTTP request authenticated with a
// bearer token of the given kind. Retries on transient failures and swaps in
// a refreshed token on 401. Returns the response body. Errors never include
// the response body to avoid leaking tokens.
func (g *GraphClient) doBearerRequest(ctx context.Context, req *http.Request, kind tokenKind) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var bodyBytes []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("reading request body for %s %s: %w", req.Method, sanitizeURL(req.URL), err)
		}
		_ = req.Body.Close()
		bodyBytes = b
	}
	reqURL := req.URL
	method := req.Method
	headers := req.Header.Clone()
	headers.Set("Authorization", "Bearer "+g.tokenFor(kind))

	build := func() (*http.Request, error) {
		var body io.Reader
		if bodyBytes != nil {
			body = bytes.NewReader(bodyBytes)
		}
		r, err := http.NewRequestWithContext(ctx, method, reqURL.String(), body)
		if err != nil {
			return nil, err
		}
		r.Header = headers.Clone()
		return r, nil
	}

	resp, err := retryDo(retryDoer{ctx: ctx, client: g.http, buildReq: build})
	if err != nil {
		return nil, fmt.Errorf("executing %s %s: %w", method, sanitizeURL(reqURL), err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if rerr := g.refreshToken(); rerr != nil {
			return nil, fmt.Errorf("%s %s returned status %d", method, sanitizeURL(reqURL), http.StatusUnauthorized)
		}
		headers.Set("Authorization", "Bearer "+g.tokenFor(kind))
		resp, err = retryDo(retryDoer{ctx: ctx, client: g.http, buildReq: build})
		if err != nil {
			return nil, fmt.Errorf("executing %s %s: %w", method, sanitizeURL(reqURL), err)
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s %s: %w", method, sanitizeURL(reqURL), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s returned status %d", method, sanitizeURL(reqURL), resp.StatusCode)
	}

	return body, nil
}

// refreshToken reloads tokens from disk via EnsureFresh and swaps them into g.
// Used after a 401 to recover from an expired access token.
func (g *GraphClient) refreshToken() error {
	tokens, err := auth.Load()
	if err != nil {
		return err
	}
	fresh, err := auth.EnsureFresh(tokens)
	if err != nil {
		return err
	}
	if fresh.Graph != "" {
		g.token = fresh.Graph
	}
	if fresh.Assignments != "" {
		g.assignmentsToken = fresh.Assignments
	}
	return nil
}

// GetClasses returns all education classes the user is enrolled in.
// Uses the assignments.onenote.com API which works without EduAssignments admin consent.
func (g *GraphClient) GetClasses(ctx context.Context) ([]EducationClass, error) {
	body, err := g.assignmentsRequest(ctx, assignmentsBaseURL+"/me/classes")
	if err != nil {
		return nil, fmt.Errorf("fetching classes: %w", err)
	}

	var resp graphListResponse[EducationClass]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing classes: %w", err)
	}

	for i := range resp.Value {
		if resp.Value[i].DisplayName == "" && resp.Value[i].Name != "" {
			resp.Value[i].DisplayName = resp.Value[i].Name
		}
	}

	return resp.Value, nil
}

// GetAssignments returns all assignments for a class.
func (g *GraphClient) GetAssignments(ctx context.Context, classID string) ([]Assignment, error) {
	body, err := g.assignmentsRequest(ctx, assignmentsBaseURL+"/classes/"+classID+"/assignments")
	if err != nil {
		return nil, fmt.Errorf("fetching assignments: %w", err)
	}

	var resp graphListResponse[Assignment]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing assignments: %w", err)
	}

	for i := range resp.Value {
		resp.Value[i].ClassID = classID
	}

	return resp.Value, nil
}

// GetAllAssignments returns assignments across all classes.
func (g *GraphClient) GetAllAssignments(ctx context.Context) ([]Assignment, error) {
	classes, err := g.GetClasses(ctx)
	if err != nil {
		return nil, err
	}

	var all []Assignment
	for _, class := range classes {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		assignments, err := g.GetAssignments(ctx, class.ID)
		if err != nil {
			continue // skip classes that fail
		}
		for i := range assignments {
			assignments[i].ClassName = class.GetDisplayName()
		}
		all = append(all, assignments...)
	}

	return all, nil
}

// GetSubmission returns the user's submission for an assignment.
func (g *GraphClient) GetSubmission(ctx context.Context, classID, assignmentID string) (*AssignmentSubmission, error) {
	body, err := g.assignmentsRequest(ctx, assignmentsBaseURL+"/classes/"+classID+"/assignments/"+assignmentID+"/submissions")
	if err != nil {
		return nil, err
	}

	var resp graphListResponse[AssignmentSubmission]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	if len(resp.Value) == 0 {
		return nil, fmt.Errorf("no submission found")
	}

	return &resp.Value[0], nil
}

// GetSubmissionResources returns the resources (files/links) attached to a submission.
func (g *GraphClient) GetSubmissionResources(ctx context.Context, classID, assignmentID, submissionID string) ([]SubmissionResource, error) {
	body, err := g.assignmentsRequest(ctx, assignmentsBaseURL+"/classes/"+classID+"/assignments/"+assignmentID+"/submissions/"+submissionID+"/resources")
	if err != nil {
		return nil, err
	}

	var resp graphListResponse[SubmissionResource]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	return resp.Value, nil
}

// GetAssignmentsWithStatus returns assignments enriched with YOUR submission status.
func (g *GraphClient) GetAssignmentsWithStatus(ctx context.Context, classID string) ([]AssignmentWithSubmission, error) {
	assignments, err := g.GetAssignments(ctx, classID)
	if err != nil {
		return nil, err
	}

	var result []AssignmentWithSubmission
	for _, a := range assignments {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		aws := AssignmentWithSubmission{Assignment: a, SubmissionStatus: "not submitted"}

		sub, err := g.GetSubmission(ctx, classID, a.ID)
		if err == nil && sub != nil {
			aws.SubmissionStatus = sub.Status
			aws.SubmittedDateTime = sub.SubmittedDateTime
			aws.SubmissionID = sub.ID

			resources, err := g.GetSubmissionResources(ctx, classID, a.ID, sub.ID)
			if err == nil {
				aws.SubmittedResources = resources
			}
		}

		result = append(result, aws)
	}

	return result, nil
}

// GetAllAssignmentsWithStatus returns enriched assignments across all classes.
func (g *GraphClient) GetAllAssignmentsWithStatus(ctx context.Context) ([]AssignmentWithSubmission, error) {
	classes, err := g.GetClasses(ctx)
	if err != nil {
		return nil, err
	}

	var all []AssignmentWithSubmission
	for _, class := range classes {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		assignments, err := g.GetAssignmentsWithStatus(ctx, class.ID)
		if err != nil {
			continue
		}
		for i := range assignments {
			assignments[i].ClassName = class.GetDisplayName()
		}
		all = append(all, assignments...)
	}

	return all, nil
}

// SetupSubmissionResourcesFolder sets up the resources folder for a submission.
// This must be called before uploading files.
func (g *GraphClient) SetupSubmissionResourcesFolder(ctx context.Context, classID, assignmentID, submissionID string) error {
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/SetUpResourcesFolder"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating setup request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if _, err := g.doBearerRequest(ctx, req, tokenAssignments); err != nil {
		return fmt.Errorf("setting up resources folder: %w", err)
	}

	return nil
}

// UploadSubmissionResource uploads a file as a submission resource.
//
// Microsoft's educationFileResource needs the file to already live in
// SharePoint before it can be registered against the submission. The flow is:
//
//  1. GET the submission to find its `resourceFolderUrl` (SharePoint sharing URL).
//  2. Resolve that URL to a drive item via `/shares/{encoded}/driveItem`.
//  3. PUT the file bytes to the drive item's folder.
//  4. POST the educationFileResource metadata pointing at the uploaded webUrl.
//
// Caller must invoke SetupSubmissionResourcesFolder first so the sharepoint
// folder exists.
func (g *GraphClient) UploadSubmissionResource(ctx context.Context, classID, assignmentID, submissionID, fileName string, fileData []byte) (*SubmissionResource, error) {
	folderURL, err := g.getSubmissionResourceFolderURL(ctx, classID, assignmentID, submissionID)
	if err != nil {
		return nil, fmt.Errorf("resolving resource folder: %w", err)
	}

	folderItem, err := g.getDriveItemFromSharingURL(ctx, folderURL)
	if err != nil {
		return nil, fmt.Errorf("resolving sharepoint folder: %w", err)
	}
	if folderItem.ParentReference == nil || folderItem.ParentReference.DriveID == "" {
		return nil, fmt.Errorf("folder driveItem missing parent reference")
	}

	uploaded, err := g.uploadToDriveFolder(ctx, folderItem.ParentReference.DriveID, folderItem.ID, fileName, fileData)
	if err != nil {
		return nil, fmt.Errorf("uploading file to sharepoint: %w", err)
	}

	return g.registerEducationFileResource(ctx, classID, assignmentID, submissionID, fileName, uploaded.WebURL)
}

// getSubmissionResourceFolderURL returns the SharePoint sharing URL where a
// submission's resources live. SetUpResourcesFolder must have been called.
func (g *GraphClient) getSubmissionResourceFolderURL(ctx context.Context, classID, assignmentID, submissionID string) (string, error) {
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID
	body, err := g.assignmentsRequest(ctx, endpoint)
	if err != nil {
		return "", fmt.Errorf("fetching submission: %w", err)
	}

	var sub struct {
		ResourcesFolderURL string `json:"resourcesFolderUrl"`
		ResourceFolderURL  string `json:"resourceFolderUrl"`
	}
	if err := json.Unmarshal(body, &sub); err != nil {
		return "", fmt.Errorf("parsing submission: %w", err)
	}
	folder := sub.ResourcesFolderURL
	if folder == "" {
		folder = sub.ResourceFolderURL
	}
	if folder == "" {
		return "", fmt.Errorf("submission has no resources folder url (run SetUpResourcesFolder first)")
	}
	return folder, nil
}

// getDriveItemFromSharingURL resolves a folder reference returned by the
// assignments API to its driveItem. Two shapes are observed in the wild:
//
//  1. A direct Graph reference: `https://graph.microsoft.com/v1.0/drives/{driveID}/items/{itemID}`
//     which is fetched directly and does not need the /shares lookup.
//  2. A SharePoint sharing URL (`https://{tenant}.sharepoint.com/...`) which
//     must be base64url-encoded and fetched via `/shares/{u!id}/driveItem`.
func (g *GraphClient) getDriveItemFromSharingURL(ctx context.Context, sharingURL string) (*DriveItem, error) {
	var endpoint string
	if strings.HasPrefix(sharingURL, "https://graph.microsoft.com/") {
		endpoint = sharingURL
	} else {
		shareID := encodeSharingURL(sharingURL)
		endpoint = graphBaseURL + "/shares/" + shareID + "/driveItem"
	}

	body, err := g.graphRequest(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("resolving share %q: %w", sharingURL, err)
	}

	var item DriveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("parsing share driveItem: %w", err)
	}

	if item.ParentReference == nil || item.ParentReference.DriveID == "" {
		if driveID := extractDriveIDFromGraphURL(sharingURL); driveID != "" {
			if item.ParentReference == nil {
				item.ParentReference = &ParentReference{}
			}
			item.ParentReference.DriveID = driveID
		}
	}
	return &item, nil
}

// extractDriveIDFromGraphURL pulls the driveID out of a graph.microsoft.com
// drives/{driveID}/items/{itemID} URL. Returns empty string if not matched.
func extractDriveIDFromGraphURL(u string) string {
	const marker = "/drives/"
	i := strings.Index(u, marker)
	if i < 0 {
		return ""
	}
	rest := u[i+len(marker):]
	end := strings.Index(rest, "/")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// uploadToDriveFolder uploads bytes to a specific folder in a specific drive
// using the simple PUT content endpoint. Suitable for files up to a few MB.
func (g *GraphClient) uploadToDriveFolder(ctx context.Context, driveID, folderID, fileName string, data []byte) (*DriveItem, error) {
	endpoint := graphBaseURL + "/drives/" + driveID + "/items/" + folderID + ":/" + url.PathEscape(fileName) + ":/content"

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	body, err := g.doBearerRequest(ctx, req, tokenGraph)
	if err != nil {
		return nil, fmt.Errorf("uploading file: %w", err)
	}

	var item DriveItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("parsing uploaded item: %w", err)
	}
	return &item, nil
}

// registerEducationFileResource attaches an already-uploaded sharepoint file
// to a submission by POSTing its webUrl as an educationFileResource.
func (g *GraphClient) registerEducationFileResource(ctx context.Context, classID, assignmentID, submissionID, fileName, fileURL string) (*SubmissionResource, error) {
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/resources"

	resourceBody := map[string]any{
		"resource": map[string]any{
			"@odata.type": "#microsoft.graph.educationFileResource",
			"displayName": fileName,
			"fileUrl":     fileURL,
		},
	}

	bodyBytes, err := json.Marshal(resourceBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling resource request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating resource request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	body, err := g.doBearerRequest(ctx, req, tokenAssignments)
	if err != nil {
		return nil, fmt.Errorf("adding submission resource: %w", err)
	}

	var resource SubmissionResource
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("parsing resource response: %w", err)
	}
	return &resource, nil
}

// encodeSharingURL encodes a SharePoint sharing URL for use with the Graph
// `/shares/{id}/driveItem` endpoint. The format is `u!<base64url no padding>`.
func encodeSharingURL(sharingURL string) string {
	raw := base64.StdEncoding.EncodeToString([]byte(sharingURL))
	raw = strings.TrimRight(raw, "=")
	raw = strings.ReplaceAll(raw, "+", "-")
	raw = strings.ReplaceAll(raw, "/", "_")
	return "u!" + raw
}

// SubmitAssignment submits a student's assignment submission.
func (g *GraphClient) SubmitAssignment(ctx context.Context, classID, assignmentID, submissionID string) error {
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/submit"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if _, err := g.doBearerRequest(ctx, req, tokenAssignments); err != nil {
		return fmt.Errorf("submitting assignment: %w", err)
	}

	return nil
}
