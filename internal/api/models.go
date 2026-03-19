package api

// ConversationResponse is the top-level response from the teams/users/me endpoint.
type ConversationResponse struct {
	Teams []Team `json:"teams"`
	Chats []Chat `json:"chats"`
}

// Team represents a Microsoft Teams team.
// Non-essential fields use `any` due to Microsoft's inconsistent API types.
type Team struct {
	DisplayName          string    `json:"displayName"`
	ID                   string    `json:"id"`
	Description          string    `json:"description"`
	IsArchived           bool      `json:"isArchived"`
	Channels             []Channel `json:"channels"`
	TenantID             string    `json:"tenantId"`
	TeamSiteInformation  TeamSite  `json:"teamSiteInformation"`
	MembershipSummary    *MembershipSummary `json:"membershipSummary,omitempty"`
	// Flexible fields - MS API returns mixed types
	TeamType             any       `json:"teamType"`
	ThreadVersion        any       `json:"threadVersion,omitempty"`
	IsFavorite           any       `json:"isFavorite"`
	IsCreator            any       `json:"isCreator"`
	IsMember             any       `json:"isMember"`
	IsFollowed           any       `json:"isFollowed"`
	MembershipVersion    any       `json:"membershipVersion"`
	AccessType           any       `json:"accessType"`
	PictureETag          any       `json:"pictureETag"`
	SmtpAddress          any       `json:"smtpAddress"`
	Classification       any       `json:"classification"`
}

// TeamSite holds SharePoint site info for a team.
type TeamSite struct {
	GroupID   string `json:"groupId"`
	SiteURL   string `json:"sharepointSiteUrl"`
}

// MembershipSummary holds member count info.
type MembershipSummary struct {
	BotCount    int `json:"botCount"`
	MemberCount int `json:"memberCount"`
	GuestCount  int `json:"guestCount"`
	TotalCount  int `json:"totalMemberCount"`
}

// Channel represents a channel within a team.
// Many fields use `any` because Microsoft's API returns inconsistent types
// (string vs number vs null for the same field across different channels).
type Channel struct {
	ID               string   `json:"id"`
	DisplayName      string   `json:"displayName"`
	Description      string   `json:"description"`
	ParentTeamID     string   `json:"parentTeamId"`
	IsGeneral        bool     `json:"isGeneral"`
	IsFavorite       bool     `json:"isFavorite"`
	IsMember         bool     `json:"isMember"`
	IsFollowed       bool     `json:"isFollowed"`
	IsMuted          bool     `json:"isMuted"`
	IsArchived       bool     `json:"isArchived"`
	IsReadOnly       bool     `json:"isReadOnly"`
	ChannelType      any      `json:"channelType"`
	ThreadVersion    any      `json:"threadVersion,omitempty"`
	MembershipType   any      `json:"membershipType"`
	TenantID         string   `json:"tenantId"`
	LastMessage      *Message `json:"lastMessage,omitempty"`
	MemberRole       any      `json:"memberRole"`
	MembershipExpiry any      `json:"membershipExpiry"`
	Version          any      `json:"version"`
	CreationTime     any      `json:"creationTime"`
}

// Chat represents a DM or group chat.
// Most fields use `any` because Microsoft's API returns inconsistent types.
type Chat struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	ChatType     any          `json:"chatType"`
	ChatSubType  any          `json:"chatSubType"`
	IsOneOnOne   bool         `json:"isOneOnOne"`
	IsRead       any          `json:"isRead"`
	IsDisabled   any          `json:"isDisabled"`
	Hidden       any          `json:"hidden"`
	CreatedAt    any          `json:"createdAt"`
	LastMessage  *Message     `json:"lastMessage,omitempty"`
	Members      []ChatMember `json:"members"`
	ThreadVersion any         `json:"threadVersion,omitempty"`
	Version      any          `json:"version"`
	TenantID     string       `json:"tenantId"`
	InteropType  any          `json:"interopType"`
	ChatStatus   any          `json:"chatStatus"`
	ConsumptionHorizon any    `json:"consumptionHorizon,omitempty"`
}

// ChatMember represents a member of a chat.
type ChatMember struct {
	Mri          string `json:"mri"`
	FriendlyName string `json:"friendlyName"`
	Role         string `json:"role"`
	TenantID     string `json:"tenantId"`
	IsMuted      bool   `json:"isMuted"`
	ObjectID     string `json:"objectId"`
	UserType     string `json:"userType"`
}

// ConsumptionHorizon tracks read state for a chat.
type ConsumptionHorizon struct {
	OriginalArrivalTime any `json:"originalArrivalTime"`
	TimeStamp           any `json:"timeStamp"`
	ClientMessageID     any `json:"clientMessageId"`
}

// Message represents a message (used for last message in channels/chats).
type Message struct {
	MessageType     string `json:"messagetype"`
	Content         string `json:"content"`
	ClientMessageID string `json:"clientmessageid"`
	ImDisplayName   string `json:"imdisplayname"`
	ID              string `json:"id"`
	ComposeTime     string `json:"composetime"`
	OriginalArrivalTime string `json:"originalarrivaltime"`
	From            string `json:"from"`
	SequenceID      any    `json:"sequenceId"`
	Version         any    `json:"version"`
	ContentType     string `json:"contenttype"`
}

// MessagesResponse is the response from the messages endpoint.
type MessagesResponse struct {
	Messages []ChatMessage    `json:"messages"`
	Metadata *MessageMetadata `json:"_metadata,omitempty"`
}

// MessageMetadata contains pagination info.
type MessageMetadata struct {
	BackwardLink string `json:"backwardLink"`
	SyncState    string `json:"syncState"`
}

// ChatMessage is a full message from the messages endpoint.
type ChatMessage struct {
	ID              string            `json:"id"`
	SequenceID      int64             `json:"sequenceId"`
	ClientMessageID string            `json:"clientmessageid"`
	Version         string            `json:"version"`
	ConversationID  string            `json:"conversationid"`
	ConversationLink string           `json:"conversationLink"`
	Type            string            `json:"type"`
	MessageType     string            `json:"messagetype"`
	ContentType     string            `json:"contenttype"`
	Content         string            `json:"content"`
	From            string            `json:"from"`
	ImDisplayName   string            `json:"imdisplayname"`
	ComposeTime     string            `json:"composetime"`
	OriginalArrivalTime string        `json:"originalarrivaltime"`
	Properties      map[string]any    `json:"properties,omitempty"`
	AnnotationsSummary *AnnotationsSummary `json:"annotationsSummary,omitempty"`
	IsFromMe        bool              `json:"isFromMe"`
	AmsReferences   []any             `json:"amsreferences,omitempty"`
}

// ParentMessageID returns the parent message ID if this message is a reply, empty string otherwise.
func (m *ChatMessage) ParentMessageID() string {
	if m.Properties == nil {
		return ""
	}
	if pid, ok := m.Properties["parentMessageId"].(string); ok {
		return pid
	}
	return ""
}

// GetEmotions returns a map of emotion type to count from the message annotations.
func (m *ChatMessage) GetEmotions() map[string]int {
	result := make(map[string]int)

	// Check annotationsSummary.emotions (can be array or object)
	if m.AnnotationsSummary != nil && m.AnnotationsSummary.Emotions != nil {
		switch e := m.AnnotationsSummary.Emotions.(type) {
		case []any:
			for _, item := range e {
				if em, ok := item.(map[string]any); ok {
					key, _ := em["key"].(string)
					count, _ := em["users_count"].(float64)
					if key != "" {
						result[key] = int(count)
					}
				}
			}
		case map[string]any:
			for key, val := range e {
				if count, ok := val.(float64); ok {
					result[key] = int(count)
				}
			}
		}
	}

	// Also check properties.emotions as fallback
	if m.Properties != nil {
		if emotions, ok := m.Properties["emotions"].(map[string]any); ok {
			for key, val := range emotions {
				if count, ok := val.(float64); ok {
					result[key] = int(count)
				}
			}
		}
	}

	return result
}

// AnnotationsSummary holds reaction/annotation data for a message.
type AnnotationsSummary struct {
	Emotions any `json:"emotions,omitempty"` // can be []EmotionCount or map[string]any, MS is inconsistent
}

