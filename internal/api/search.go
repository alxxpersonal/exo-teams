package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SearchHit represents a single search result.
type SearchHit struct {
	Summary  string         `json:"summary"`
	Resource map[string]any `json:"resource"`
}

// SearchHitsContainer wraps a set of search hits.
type SearchHitsContainer struct {
	Hits     []SearchHit `json:"hits"`
	Total    int         `json:"total"`
	MoreAvailable bool  `json:"moreResultsAvailable"`
}

// SearchResponse is the top-level Graph search response.
type SearchResponse struct {
	Value []SearchHitsContainer `json:"value"`
}

// SearchMessages searches across Teams messages and files using the Graph search API.
// Uses "message" and "driveItem" entity types which work without Chat.Read scope.
// "message" returns Teams notification emails (partial workaround).
// "driveItem" returns files shared in Teams.
func (g *GraphClient) SearchMessages(query string) ([]SearchHit, error) {
	reqBody := map[string]any{
		"requests": []map[string]any{
			{
				"entityTypes": []string{"message", "driveItem"},
				"query": map[string]string{
					"queryString": query,
				},
				"from": 0,
				"size": 25,
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling search request: %w", err)
	}

	req, err := http.NewRequest("POST", graphBaseURL+"/search/query", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating search request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search returned status %d: %s", resp.StatusCode, string(body))
	}

	var searchResp SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	var hits []SearchHit
	for _, container := range searchResp.Value {
		hits = append(hits, container.Hits...)
	}

	return hits, nil
}
