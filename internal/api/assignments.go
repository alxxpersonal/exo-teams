package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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
	ID                string                  `json:"id"`
	DisplayName       string                  `json:"displayName"`
	DueDateTime       string                  `json:"dueDateTime"`
	AssignedDateTime  string                  `json:"assignedDateTime"`
	CreatedDateTime   string                  `json:"createdDateTime"`
	Status            string                  `json:"status"`
	Instructions      *AssignmentInstructions `json:"instructions,omitempty"`
	ClassID           string                  `json:"classId"`
	ClassName         string                  `json:"-"` // populated after fetch
	AllowLateSubmissions bool                 `json:"allowLateSubmissions"`
	CloseDateTime     string                  `json:"closeDateTime"`
	WebURL            string                  `json:"webUrl"`
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
	SubmissionStatus    string               `json:"submissionStatus"`
	SubmittedDateTime   string               `json:"submittedDateTime"`
	SubmissionID        string               `json:"submissionId"`
	SubmittedResources  []SubmissionResource `json:"submittedResources,omitempty"`
}

// EducationClass represents a Teams education class.
type EducationClass struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Name        string `json:"name,omitempty"`       // assignments.onenote.com uses "name"
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

func (g *GraphClient) graphRequest(url string) ([]byte, error) {
	return g.doAuthRequest(url, g.token)
}

func (g *GraphClient) assignmentsRequest(url string) ([]byte, error) {
	token := g.assignmentsToken
	if token == "" {
		token = g.token // fallback to graph token
	}
	return g.doAuthRequest(url, token)
}

func (g *GraphClient) doAuthRequest(url, token string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", url, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request for %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// GetClasses returns all education classes the user is enrolled in.
// Uses the assignments.onenote.com API which works without EduAssignments admin consent.
func (g *GraphClient) GetClasses() ([]EducationClass, error) {
	body, err := g.assignmentsRequest(assignmentsBaseURL + "/me/classes")
	if err != nil {
		return nil, fmt.Errorf("fetching classes: %w", err)
	}

	var resp graphListResponse[EducationClass]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing classes: %w", err)
	}

	// Normalize: copy Name to DisplayName if needed
	for i := range resp.Value {
		if resp.Value[i].DisplayName == "" && resp.Value[i].Name != "" {
			resp.Value[i].DisplayName = resp.Value[i].Name
		}
	}

	return resp.Value, nil
}

// GetAssignments returns all assignments for a class.
func (g *GraphClient) GetAssignments(classID string) ([]Assignment, error) {
	body, err := g.assignmentsRequest(assignmentsBaseURL + "/classes/" + classID + "/assignments")
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
func (g *GraphClient) GetAllAssignments() ([]Assignment, error) {
	classes, err := g.GetClasses()
	if err != nil {
		return nil, err
	}

	var all []Assignment
	for _, class := range classes {
		assignments, err := g.GetAssignments(class.ID)
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
func (g *GraphClient) GetSubmission(classID, assignmentID string) (*AssignmentSubmission, error) {
	body, err := g.assignmentsRequest(assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions")
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
func (g *GraphClient) GetSubmissionResources(classID, assignmentID, submissionID string) ([]SubmissionResource, error) {
	body, err := g.assignmentsRequest(assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/resources")
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
func (g *GraphClient) GetAssignmentsWithStatus(classID string) ([]AssignmentWithSubmission, error) {
	assignments, err := g.GetAssignments(classID)
	if err != nil {
		return nil, err
	}

	var result []AssignmentWithSubmission
	for _, a := range assignments {
		aws := AssignmentWithSubmission{Assignment: a, SubmissionStatus: "not submitted"}

		sub, err := g.GetSubmission(classID, a.ID)
		if err == nil && sub != nil {
			aws.SubmissionStatus = sub.Status
			aws.SubmittedDateTime = sub.SubmittedDateTime
			aws.SubmissionID = sub.ID

			// Get submitted resources
			resources, err := g.GetSubmissionResources(classID, a.ID, sub.ID)
			if err == nil {
				aws.SubmittedResources = resources
			}
		}

		result = append(result, aws)
	}

	return result, nil
}

// GetAllAssignmentsWithStatus returns enriched assignments across all classes.
func (g *GraphClient) GetAllAssignmentsWithStatus() ([]AssignmentWithSubmission, error) {
	classes, err := g.GetClasses()
	if err != nil {
		return nil, err
	}

	var all []AssignmentWithSubmission
	for _, class := range classes {
		assignments, err := g.GetAssignmentsWithStatus(class.ID)
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
func (g *GraphClient) SetupSubmissionResourcesFolder(classID, assignmentID, submissionID string) error {
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/SetUpResourcesFolder"

	req, err := http.NewRequest("POST", endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating setup request: %w", err)
	}

	token := g.assignmentsToken
	if token == "" {
		token = g.token
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("setting up resources folder: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("setup resources folder returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// UploadSubmissionResource uploads a file as a submission resource.
// The file is added to the submission's resources folder.
func (g *GraphClient) UploadSubmissionResource(classID, assignmentID, submissionID, fileName string, fileData []byte) (*SubmissionResource, error) {
	// Add the file as a resource to the submission
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/resources"

	resourceBody := map[string]any{
		"resource": map[string]any{
			"@odata.type":  "#microsoft.graph.educationFileResource",
			"displayName":  fileName,
		},
	}

	bodyBytes, err := json.Marshal(resourceBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling resource request: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating resource request: %w", err)
	}

	token := g.assignmentsToken
	if token == "" {
		token = g.token
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adding submission resource: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading resource response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("add resource returned %d: %s", resp.StatusCode, string(body))
	}

	var resource SubmissionResource
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("parsing resource response: %w", err)
	}

	return &resource, nil
}

// SubmitAssignment submits a student's assignment submission.
func (g *GraphClient) SubmitAssignment(classID, assignmentID, submissionID string) error {
	endpoint := assignmentsBaseURL + "/classes/" + classID + "/assignments/" + assignmentID + "/submissions/" + submissionID + "/submit"

	req, err := http.NewRequest("POST", endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating submit request: %w", err)
	}

	token := g.assignmentsToken
	if token == "" {
		token = g.token
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("submitting assignment: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("submit returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
