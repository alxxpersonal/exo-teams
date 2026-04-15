package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// UserProfile represents a user's profile info.
type UserProfile struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Mri         string `json:"mri"`
}

// ResolveUserProfiles fetches display names for a list of MRI identifiers via Graph API.
// MRIs look like "8:orgid:uuid-here". We extract the UUID and query Graph.
func (c *Client) ResolveUserProfiles(ctx context.Context, mris []string) (map[string]string, error) {
	if len(mris) == 0 {
		return nil, nil
	}

	result := make(map[string]string)

	for _, mri := range mris {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !strings.HasPrefix(mri, "8:orgid:") {
			continue
		}
		userID := strings.TrimPrefix(mri, "8:orgid:")

		endpoint := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s?$select=displayName", userID)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}

		req.Header.Set("Authorization", "Bearer "+c.tokens.Graph)

		respBody, err := c.doRequest(ctx, req)
		if err != nil {
			continue
		}

		var profile struct {
			DisplayName string `json:"displayName"`
		}
		if err := json.Unmarshal(respBody, &profile); err != nil {
			continue
		}

		if profile.DisplayName != "" {
			result[mri] = profile.DisplayName
		}
	}

	return result, nil
}

// ResolveChatNames resolves member names for chats that have empty titles.
func (c *Client) ResolveChatNames(ctx context.Context, chats []Chat) {
	mriSet := make(map[string]bool)
	for _, chat := range chats {
		if chat.Title == "" {
			for _, m := range chat.Members {
				if m.Mri != "" {
					mriSet[m.Mri] = true
				}
			}
		}
	}

	if len(mriSet) == 0 {
		return
	}

	mris := make([]string, 0, len(mriSet))
	for mri := range mriSet {
		mris = append(mris, mri)
	}

	names, err := c.ResolveUserProfiles(ctx, mris)
	if err != nil {
		return // silently fail, names just stay empty
	}

	for i := range chats {
		for j := range chats[i].Members {
			if name, ok := names[chats[i].Members[j].Mri]; ok {
				chats[i].Members[j].FriendlyName = name
			}
		}
	}
}
