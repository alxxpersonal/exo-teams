package api

import (
	"context"
	"fmt"
	"net/http"
)

const (
	teamsUsersMe = teamsBaseURL + "/teams/users/me?isPrefetch=false&enableMembershipSummary=true"
)

// GetConversations fetches all teams and chats for the current user.
// Uses the chatsvcagg token for authorization.
func (c *Client) GetConversations(ctx context.Context) (*ConversationResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, teamsUsersMe, nil)
	if err != nil {
		return nil, fmt.Errorf("creating conversations request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.tokens.ChatSvcAgg)
	req.Header.Set("Accept", "application/json")

	var resp ConversationResponse
	if err := c.doRequestJSON(ctx, req, &resp); err != nil {
		return nil, fmt.Errorf("fetching conversations: %w", err)
	}

	return &resp, nil
}

// GetTeams returns all teams the user belongs to.
func (c *Client) GetTeams(ctx context.Context) ([]Team, error) {
	conv, err := c.GetConversations(ctx)
	if err != nil {
		return nil, err
	}
	return conv.Teams, nil
}

// GetChats returns all chats (DMs and group chats).
func (c *Client) GetChats(ctx context.Context) ([]Chat, error) {
	conv, err := c.GetConversations(ctx)
	if err != nil {
		return nil, err
	}
	return conv.Chats, nil
}
