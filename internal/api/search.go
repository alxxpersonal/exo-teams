package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SearchHit represents a single search result.
type SearchHit struct {
	Summary  string         `json:"summary"`
	Resource map[string]any `json:"resource"`
}

// SearchHitsContainer wraps a set of search hits.
type SearchHitsContainer struct {
	Hits          []SearchHit `json:"hits"`
	Total         int         `json:"total"`
	MoreAvailable bool        `json:"moreResultsAvailable"`
}

// SearchResponse is the top-level Graph search response.
type SearchResponse struct {
	Value []SearchHitsContainer `json:"value"`
}

// SearchMessages searches across Teams messages and files using the Graph search API.
// Uses "message" and "driveItem" entity types which work without Chat.Read scope.
// "message" returns Teams notification emails (partial workaround).
// "driveItem" returns files shared in Teams.
func (g *GraphClient) SearchMessages(ctx context.Context, query string) ([]SearchHit, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphBaseURL+"/search/query", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	body, err := g.doBearerRequest(ctx, req, tokenGraph)
	if err != nil {
		return nil, fmt.Errorf("executing search: %w", err)
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
