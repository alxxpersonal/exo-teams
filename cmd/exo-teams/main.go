package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/alxxpersonal/exo-teams/internal/api"
	"github.com/alxxpersonal/exo-teams/internal/auth"
)

var (
	jsonOutput bool
	htmlTagRe  = regexp.MustCompile(`<[^>]*>`)

	// timeFormats lists ISO 8601 date-time formats used for parsing timestamps
	// throughout the application. Shared between formatTimestamp and filterMessagesSince.
	timeFormats = []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.0000000Z",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
	}
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "exo-teams",
		Short: "CLI for Microsoft Teams internal API",
		Long:  "Read and write Microsoft Teams data using the internal API. No admin consent, no Graph API.",
	}

	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")

	rootCmd.AddCommand(
		authCmd(),
		whoamiCmd(),
		listTeamsCmd(),
		listChatsCmd(),
		getMessagesCmd(),
		getChatCmd(),
		sendCmd(),
		sendFileCmd(),
		newDMCmd(),
		assignmentsCmd(),
		submitCmd(),
		filesCmd(),
		uploadCmd(),
		downloadCmd(),
		calendarCmd(),
		activityCmd(),
		searchCmd(),
		markReadCmd(),
		unreadCmd(),
		deadlinesCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- Helpers ---

// loadGraphToken loads auth tokens and validates that a Graph token is present.
func loadGraphToken() (*auth.Tokens, error) {
	tokens, err := auth.Load()
	if err != nil {
		return nil, err
	}

	if tokens.Graph == "" {
		return nil, fmt.Errorf("no graph token - run 'exo-teams auth'")
	}

	return tokens, nil
}

// findTeamBySearch searches the user's teams for one matching the given search string.
// Returns the team's group ID, display name, and any error.
func findTeamBySearch(client *api.Client, search string) (groupID, teamName string, err error) {
	teams, err := client.GetTeams()
	if err != nil {
		return "", "", err
	}

	for _, t := range teams {
		if strings.Contains(strings.ToLower(t.DisplayName), strings.ToLower(search)) {
			return t.TeamSiteInformation.GroupID, t.DisplayName, nil
		}
	}

	return "", "", fmt.Errorf("no team found matching %q", search)
}

// --- Auth ---

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with Microsoft Teams",
		Long:  "Login via browser OAuth, import from fossteams, or refresh existing tokens.",
		RunE: func(cmd *cobra.Command, args []string) error {
			importFlag, _ := cmd.Flags().GetBool("import")
			refreshFlag, _ := cmd.Flags().GetBool("refresh")

			if importFlag {
				fmt.Fprintln(os.Stderr, "importing tokens from fossteams...")
				if err := auth.ImportFromFossteams(); err != nil {
					return fmt.Errorf("import failed: %w", err)
				}
				fmt.Fprintln(os.Stderr, "tokens imported successfully")
			} else if refreshFlag {
				fmt.Fprintln(os.Stderr, "refreshing tokens...")
				if err := auth.RefreshTokens(); err != nil {
					return fmt.Errorf("refresh failed: %w", err)
				}
				return nil
			} else {
				// Default: native OAuth login
				if err := auth.Login(); err != nil {
					return fmt.Errorf("login failed: %w", err)
				}
				return nil
			}

			fmt.Fprintln(os.Stderr, "token status:")
			if err := auth.CheckTokens(); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().Bool("import", false, "import tokens from fossteams (~/.config/fossteams/)")
	cmd.Flags().Bool("refresh", false, "refresh expired tokens using saved refresh token")
	return cmd
}

// --- List Teams ---

func listTeamsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-teams",
		Short: "List all teams and their channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			teams, err := client.GetTeams()
			if err != nil {
				return fmt.Errorf("fetching teams: %w", err)
			}

			if jsonOutput {
				return outputJSON(teams)
			}

			for _, team := range teams {
				archived := ""
				if team.IsArchived {
					archived = " [archived]"
				}
				fmt.Printf("== %s%s ==\n", team.DisplayName, archived)
				if team.Description != "" {
					fmt.Printf("   %s\n", team.Description)
				}
				for _, ch := range team.Channels {
					general := ""
					if ch.IsGeneral {
						general = " (general)"
					}
					fmt.Printf("   #%-30s %s%s\n", ch.DisplayName, ch.ID, general)
				}
				fmt.Println()
			}

			return nil
		},
	}
}

// --- List Chats ---

func listChatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-chats",
		Short: "List all DMs and group chats",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			chats, err := client.GetChats()
			if err != nil {
				return fmt.Errorf("fetching chats: %w", err)
			}

			// Resolve DM names
			client.ResolveChatNames(chats)

			if jsonOutput {
				return outputJSON(chats)
			}

			for _, chat := range chats {
				chatType := "group"
				if chat.IsOneOnOne {
					chatType = "DM"
				}

				title := chat.Title
				if title == "" {
					title = formatMemberNames(chat.Members)
				}
				if title == "" {
					title = "(unnamed)"
				}

				lastMsg := ""
				if chat.LastMessage != nil && chat.LastMessage.Content != "" {
					content := stripHTML(chat.LastMessage.Content)
					if len(content) > 80 {
						content = content[:80] + "..."
					}
					sender := chat.LastMessage.ImDisplayName
					if sender != "" {
						lastMsg = fmt.Sprintf("  %s: %s", sender, content)
					} else {
						lastMsg = "  " + content
					}
				}

				readMarker := " "
				if r, ok := chat.IsRead.(bool); ok && !r {
					readMarker = "*"
				}

				fmt.Printf("%s [%-5s] %-40s %s\n", readMarker, chatType, title, chat.ID)
				if lastMsg != "" {
					fmt.Printf("         %s\n", lastMsg)
				}
			}

			return nil
		},
	}
}

// --- Get Messages ---

func getMessagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-messages <search>",
		Short: "Get messages from a channel matching search term",
		Long:  "Search across all teams for a channel matching the given term and display its messages.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			search := strings.ToLower(args[0])
			count, _ := cmd.Flags().GetInt("count")
			allPages, _ := cmd.Flags().GetBool("all")
			showReplies, _ := cmd.Flags().GetBool("replies")

			client, err := api.NewClient()
			if err != nil {
				return err
			}

			teams, err := client.GetTeams()
			if err != nil {
				return fmt.Errorf("fetching teams: %w", err)
			}

			// Find matching channel
			var matchedChannel *api.Channel
			var matchedTeam *api.Team
			for i := range teams {
				for j := range teams[i].Channels {
					ch := &teams[i].Channels[j]
					if strings.Contains(strings.ToLower(teams[i].DisplayName+" "+ch.DisplayName), search) || ch.ID == args[0] {
						matchedChannel = ch
						matchedTeam = &teams[i]
						break
					}
				}
				if matchedChannel != nil {
					break
				}
			}

			if matchedChannel == nil {
				return fmt.Errorf("no channel found matching %q", args[0])
			}

			fmt.Fprintf(os.Stderr, "fetching messages from #%s in %s...\n", matchedChannel.DisplayName, matchedTeam.DisplayName)

			var messages []api.ChatMessage
			if allPages {
				messages, err = client.GetAllMessages(matchedChannel.ID, 200, 0)
			} else {
				messages, err = client.GetAllMessages(matchedChannel.ID, count, 1)
			}
			if err != nil {
				return fmt.Errorf("fetching messages: %w", err)
			}

			// Filter by --since if provided
			since, _ := cmd.Flags().GetString("since")
			if since != "" {
				messages = filterMessagesSince(messages, since)
			}

			if jsonOutput {
				return outputJSON(messages)
			}

			if showReplies {
				printMessagesWithReplies(messages)
			} else {
				printMessages(messages)
			}
			return nil
		},
	}
	cmd.Flags().Int("count", 200, "number of messages to fetch per page")
	cmd.Flags().Bool("all", false, "fetch all messages by following pagination")
	cmd.Flags().Bool("replies", false, "show reply threads grouped and indented")
	cmd.Flags().String("since", "", "only show messages after this date (e.g. 2026-03-15)")
	return cmd
}

// --- Get Chat ---

func getChatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-chat <search>",
		Short: "Get messages from a DM or group chat",
		Long:  "Search chats by title, member name, ID, or last message content.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			search := strings.ToLower(args[0])
			count, _ := cmd.Flags().GetInt("count")
			allPages, _ := cmd.Flags().GetBool("all")
			showReplies, _ := cmd.Flags().GetBool("replies")

			client, err := api.NewClient()
			if err != nil {
				return err
			}

			chats, err := client.GetChats()
			if err != nil {
				return fmt.Errorf("fetching chats: %w", err)
			}

			// Resolve names for searching
			client.ResolveChatNames(chats)

			// Find matching chat
			var matchedChat *api.Chat
			for i := range chats {
				c := &chats[i]

				// Match by ID
				if c.ID == args[0] {
					matchedChat = c
					break
				}

				// Match by title
				if c.Title != "" && strings.Contains(strings.ToLower(c.Title), search) {
					matchedChat = c
					break
				}

				// Match by member name
				for _, m := range c.Members {
					if strings.Contains(strings.ToLower(m.FriendlyName), search) {
						matchedChat = c
						break
					}
				}
				if matchedChat != nil {
					break
				}

				// Match by last message content
				if c.LastMessage != nil && strings.Contains(strings.ToLower(c.LastMessage.Content), search) {
					matchedChat = c
					break
				}
			}

			if matchedChat == nil {
				return fmt.Errorf("no chat found matching %q", args[0])
			}

			chatTitle := matchedChat.Title
			if chatTitle == "" {
				chatTitle = formatMemberNames(matchedChat.Members)
			}
			fmt.Fprintf(os.Stderr, "fetching messages from chat: %s...\n", chatTitle)

			var messages []api.ChatMessage
			if allPages {
				messages, err = client.GetAllMessages(matchedChat.ID, 200, 0)
			} else {
				messages, err = client.GetAllMessages(matchedChat.ID, count, 1)
			}
			if err != nil {
				return fmt.Errorf("fetching messages: %w", err)
			}

			// Filter by --since if provided
			since, _ := cmd.Flags().GetString("since")
			if since != "" {
				messages = filterMessagesSince(messages, since)
			}

			if jsonOutput {
				return outputJSON(messages)
			}

			if showReplies {
				printMessagesWithReplies(messages)
			} else {
				printMessages(messages)
			}
			return nil
		},
	}
	cmd.Flags().Int("count", 200, "number of messages to fetch per page")
	cmd.Flags().Bool("all", false, "fetch all messages by following pagination")
	cmd.Flags().Bool("replies", false, "show reply threads grouped and indented")
	cmd.Flags().String("since", "", "only show messages after this date (e.g. 2026-03-15)")
	return cmd
}

// --- Send ---

func sendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "send <conversation-id> <message>",
		Short: "Send a message to a channel or chat",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := api.NewClient()
			if err != nil {
				return err
			}
			if err := client.SendMessage(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "message sent")
			return nil
		},
	}
}

// --- Send File ---

func sendFileCmd() *cobra.Command {
	var files []string

	cmd := &cobra.Command{
		Use:   "send-file <conversation-id>",
		Short: "Send one or more files to a channel or chat",
		Long:  "Send files with optional message. Use --file multiple times for multiple files.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			message, _ := cmd.Flags().GetString("message")

			if len(files) == 0 {
				return fmt.Errorf("--file is required (can be specified multiple times)")
			}

			client, err := api.NewClient()
			if err != nil {
				return err
			}

			for i, filePath := range files {
				fileData, err := os.ReadFile(filePath)
				if err != nil {
					return fmt.Errorf("reading file %s: %w", filePath, err)
				}

				fmt.Fprintf(os.Stderr, "uploading %s (%d KB)...\n", filePath, len(fileData)/1024)

				msg := ""
				if i == 0 {
					msg = message
				}

				if err := client.SendMessageWithFile(args[0], msg, filePath, fileData); err != nil {
					return fmt.Errorf("sending file %s: %w", filePath, err)
				}

				fmt.Fprintf(os.Stderr, "file sent (%d/%d)\n", i+1, len(files))
			}

			return nil
		},
	}
	cmd.Flags().StringArrayVar(&files, "file", nil, "path to file to send (can be repeated)")
	cmd.Flags().String("message", "", "optional message to include with the first file")
	return cmd
}

// --- New DM ---

func newDMCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new-dm <user-search> <message>",
		Short: "Start a new DM with a user and send a message",
		Long:  "Find a user by name in your Teams directory and start a new DM conversation.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			search := strings.ToLower(args[0])
			message := args[1]

			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			// Escape single quotes for OData filter syntax.
			sanitizedSearch := strings.ReplaceAll(args[0], "'", "''")

			// Search users in the tenant
			searchURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users?$filter=startswith(displayName,'%s')&$select=id,displayName,mail&$top=5", url.QueryEscape(sanitizedSearch))
			req, err := http.NewRequest("GET", searchURL, nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+tokens.Graph)

			httpClient := &http.Client{Timeout: 10 * time.Second}
			resp, err := httpClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)

			type graphUser struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
				Mail        string `json:"mail"`
			}
			var result struct {
				Value []graphUser `json:"value"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				return fmt.Errorf("parsing user search: %w", err)
			}

			// Find matching user
			var matchedUser *graphUser
			for i := range result.Value {
				if strings.Contains(strings.ToLower(result.Value[i].DisplayName), search) {
					matchedUser = &result.Value[i]
					break
				}
			}

			if matchedUser == nil {
				return fmt.Errorf("no user found matching %q", args[0])
			}

			fmt.Fprintf(os.Stderr, "found user: %s (%s)\n", matchedUser.DisplayName, matchedUser.Mail)

			// Create DM
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			mri := "8:orgid:" + matchedUser.ID
			fmt.Fprintln(os.Stderr, "creating conversation...")
			convID, err := client.StartNewDM(mri)
			if err != nil {
				return fmt.Errorf("creating DM: %w", err)
			}

			fmt.Fprintf(os.Stderr, "conversation created: %s\n", convID)

			// Send message
			if err := client.SendMessage(convID, message); err != nil {
				return fmt.Errorf("sending message: %w", err)
			}

			fmt.Fprintln(os.Stderr, "message sent")
			return nil
		},
	}
}

// --- Download ---

func downloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download <team-search>",
		Short: "Download a file from a team's SharePoint",
		Long:  "Download a file by specifying the team and file path within the team drive.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("path")
			outputDir, _ := cmd.Flags().GetString("output")
			driveName, _ := cmd.Flags().GetString("drive")

			if filePath == "" {
				return fmt.Errorf("--path is required (e.g. --path 'General/file.xlsx')")
			}

			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			// Find team
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			teamID, teamName, err := findTeamBySearch(client, args[0])
			if err != nil {
				return err
			}

			graphClient := api.NewGraphClient(tokens.Graph)

			var item *api.DriveItem

			if driveName != "" {
				// Find specific drive and download from it
				drives, driveErr := graphClient.GetTeamDrives(teamID)
				if driveErr != nil {
					return fmt.Errorf("fetching drives: %w", driveErr)
				}

				var driveID string
				for _, d := range drives {
					if strings.Contains(strings.ToLower(d.Name), strings.ToLower(driveName)) {
						driveID = d.ID
						break
					}
				}

				if driveID == "" {
					fmt.Fprintf(os.Stderr, "drive %q not found. available drives:\n", driveName)
					for _, d := range drives {
						fmt.Fprintf(os.Stderr, "  - %s\n", d.Name)
					}
					return fmt.Errorf("drive not found")
				}

				fmt.Fprintf(os.Stderr, "fetching file info from %s/%s...\n", teamName, filePath)
				item, err = graphClient.GetDriveFileByPath(driveID, filePath)
				if err != nil {
					return fmt.Errorf("getting file: %w", err)
				}
			} else {
				fmt.Fprintf(os.Stderr, "fetching file info from %s/%s...\n", teamName, filePath)
				item, err = graphClient.GetTeamFileByPath(teamID, filePath)
				if err != nil {
					return fmt.Errorf("getting file: %w", err)
				}
			}

			if item.DownloadURL == "" {
				return fmt.Errorf("no download URL available for this item (might be a folder)")
			}

			fmt.Fprintf(os.Stderr, "downloading %s (%d KB)...\n", item.Name, item.Size/1024)
			data, err := graphClient.DownloadFile(item.DownloadURL)
			if err != nil {
				return fmt.Errorf("downloading: %w", err)
			}

			// Sanitize the server-supplied filename to prevent path traversal.
			safeName := filepath.Base(item.Name)

			// Determine output path
			outPath := safeName
			if outputDir != "" {
				outPath = outputDir
				// If output is a directory, append filename
				info, err := os.Stat(outPath)
				if err == nil && info.IsDir() {
					outPath = filepath.Join(outPath, safeName)
				}
			}

			if err := os.WriteFile(outPath, data, 0644); err != nil {
				return fmt.Errorf("writing file: %w", err)
			}

			fmt.Fprintf(os.Stderr, "saved to %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().String("path", "", "file path within the team drive (e.g. 'General/Cat Cafe (1).xlsx')")
	cmd.Flags().String("output", "", "output directory or file path (default: current directory)")
	cmd.Flags().String("drive", "", "specific drive name (e.g. 'Class Materials')")
	return cmd
}

// --- Activity ---

func activityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "activity",
		Short: "Show recent activity feed (mentions, replies, reactions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			fmt.Fprintln(os.Stderr, "fetching activity feed...")

			items, err := client.GetActivity(30)
			if err != nil {
				return fmt.Errorf("fetching activity: %w", err)
			}

			if jsonOutput {
				return outputJSON(items)
			}

			if len(items) == 0 {
				fmt.Println("no recent activity")
				return nil
			}

			for _, item := range items {
				ts := formatTimestamp(item.ComposeTime)

				actType := ""
				sender := ""
				if item.Properties != nil && item.Properties.Activity != nil {
					actType = item.Properties.Activity.ActivityType
					sender = item.Properties.Activity.SourceUserImDisplayName
				}
				if actType == "" {
					actType = item.MessageType
				}
				if sender == "" {
					sender = item.ImDisplayName
				}
				if sender == "" {
					sender = "system"
				}

				content := stripHTML(item.Content)
				if len(content) > 120 {
					content = content[:120] + "..."
				}

				fmt.Printf("[%s] %-10s %s\n", ts, actType, sender)
				if content != "" {
					fmt.Printf("  %s\n", content)
				}
			}

			return nil
		},
	}
}

// --- Search ---

func searchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Search across Teams messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]

			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "searching for %q...\n", query)

			graphClient := api.NewGraphClient(tokens.Graph)
			hits, err := graphClient.SearchMessages(query)
			if err != nil {
				return fmt.Errorf("searching: %w", err)
			}

			if jsonOutput {
				return outputJSON(hits)
			}

			if len(hits) == 0 {
				fmt.Println("no results found")
				return nil
			}

			for _, hit := range hits {
				summary := stripHTML(hit.Summary)
				if summary == "" {
					summary = "(no preview)"
				}

				// Extract useful fields from resource
				sender := ""
				timestamp := ""
				chatName := ""
				body := ""
				itemName := ""

				if hit.Resource != nil {
					// Message-type results
					if from, ok := hit.Resource["from"].(map[string]any); ok {
						if device, ok := from["device"].(map[string]any); ok {
							if name, ok := device["displayName"].(string); ok {
								sender = name
							}
						}
						if user, ok := from["user"].(map[string]any); ok {
							if name, ok := user["displayName"].(string); ok {
								sender = name
							}
						}
					}
					// Email sender
					if emailSender, ok := hit.Resource["sender"].(map[string]any); ok {
						if addr, ok := emailSender["emailAddress"].(map[string]any); ok {
							if name, ok := addr["name"].(string); ok {
								sender = name
							}
						}
					}
					if ts, ok := hit.Resource["createdDateTime"].(string); ok {
						timestamp = formatTimestamp(ts)
					}
					if name, ok := hit.Resource["channelIdentity"].(map[string]any); ok {
						if cn, ok := name["channelId"].(string); ok {
							chatName = cn
						}
					}
					if b, ok := hit.Resource["body"].(map[string]any); ok {
						if content, ok := b["content"].(string); ok {
							body = stripHTML(content)
							if len(body) > 150 {
								body = body[:150] + "..."
							}
						}
					}
					// DriveItem results
					if name, ok := hit.Resource["name"].(string); ok {
						itemName = name
					}
					if subject, ok := hit.Resource["subject"].(string); ok && body == "" {
						body = subject
					}
					// bodyPreview for emails
					if preview, ok := hit.Resource["bodyPreview"].(string); ok && body == "" {
						body = stripHTML(preview)
						if len(body) > 150 {
							body = body[:150] + "..."
						}
					}
				}

				if timestamp != "" {
					fmt.Printf("[%s] ", timestamp)
				}
				if itemName != "" {
					fmt.Printf("[file] %s", itemName)
				} else if sender != "" {
					fmt.Printf("%s", sender)
				}
				if chatName != "" {
					fmt.Printf(" in %s", chatName)
				}
				fmt.Println()

				if body != "" {
					fmt.Printf("  %s\n", body)
				} else if summary != "" {
					fmt.Printf("  %s\n", summary)
				}
				fmt.Println()
			}

			return nil
		},
	}
}

// --- Output Helpers ---

func stripHTML(s string) string {
	result := htmlTagRe.ReplaceAllString(s, "")
	// Replace HTML entities
	result = strings.ReplaceAll(result, "&nbsp;", " ")
	result = strings.ReplaceAll(result, "&amp;", "&")
	result = strings.ReplaceAll(result, "&lt;", "<")
	result = strings.ReplaceAll(result, "&gt;", ">")
	result = strings.ReplaceAll(result, "&quot;", "\"")
	result = strings.ReplaceAll(result, "&#39;", "'")
	// Collapse whitespace
	result = strings.Join(strings.Fields(result), " ")
	return strings.TrimSpace(result)
}

func formatTimestamp(ts string) string {
	for _, f := range timeFormats {
		if t, err := time.Parse(f, ts); err == nil {
			return t.Local().Format("2006-01-02 15:04")
		}
	}

	return ts
}

func formatMemberNames(members []api.ChatMember) string {
	names := make([]string, 0, len(members))
	for _, m := range members {
		if m.FriendlyName != "" {
			names = append(names, m.FriendlyName)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, %s +%d more", names[0], names[1], len(names)-2)
}

func formatReactions(msg api.ChatMessage) string {
	emotions := msg.GetEmotions()
	if len(emotions) == 0 {
		return ""
	}

	var parts []string
	for key, count := range emotions {
		parts = append(parts, fmt.Sprintf("%s: %d", key, count))
	}
	return " [" + strings.Join(parts, ", ") + "]"
}

func printMessages(messages []api.ChatMessage) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Skip system/control messages
		if msg.MessageType != "RichText/Html" && msg.MessageType != "Text" && msg.MessageType != "RichText" {
			continue
		}

		content := stripHTML(msg.Content)
		if content == "" {
			continue
		}

		ts := formatTimestamp(msg.ComposeTime)
		sender := msg.ImDisplayName
		if sender == "" {
			sender = "unknown"
		}

		reactions := formatReactions(msg)
		fmt.Printf("[%s] %s: %s%s\n", ts, sender, content, reactions)
	}
}

func printMessagesWithReplies(messages []api.ChatMessage) {
	// Group messages: top-level messages and their replies
	type thread struct {
		root    *api.ChatMessage
		replies []*api.ChatMessage
	}

	threadMap := make(map[string]*thread)
	var topLevel []*thread

	// First pass: identify all messages by ID
	msgByID := make(map[string]*api.ChatMessage)
	for i := range messages {
		msgByID[messages[i].ID] = &messages[i]
	}

	// Second pass: group into threads
	for i := range messages {
		msg := &messages[i]

		// Skip system/control messages
		if msg.MessageType != "RichText/Html" && msg.MessageType != "Text" && msg.MessageType != "RichText" {
			continue
		}

		parentID := msg.ParentMessageID()
		if parentID == "" {
			// Top-level message
			t := &thread{root: msg}
			threadMap[msg.ID] = t
			topLevel = append(topLevel, t)
		} else {
			// Reply - attach to parent thread
			if t, ok := threadMap[parentID]; ok {
				t.replies = append(t.replies, msg)
			} else {
				// Parent not found yet, create a placeholder thread
				t := &thread{replies: []*api.ChatMessage{msg}}
				threadMap[parentID] = t
				// Check if parent exists in our messages
				if parent, ok := msgByID[parentID]; ok {
					t.root = parent
				}
				topLevel = append(topLevel, t)
			}
		}
	}

	// Print threads in reverse order (oldest first)
	for i := len(topLevel) - 1; i >= 0; i-- {
		t := topLevel[i]

		if t.root != nil {
			content := stripHTML(t.root.Content)
			if content == "" {
				continue
			}
			ts := formatTimestamp(t.root.ComposeTime)
			sender := t.root.ImDisplayName
			if sender == "" {
				sender = "unknown"
			}
			reactions := formatReactions(*t.root)
			fmt.Printf("[%s] %s: %s%s\n", ts, sender, content, reactions)
		}

		// Print replies indented
		for j := len(t.replies) - 1; j >= 0; j-- {
			reply := t.replies[j]
			content := stripHTML(reply.Content)
			if content == "" {
				continue
			}
			ts := formatTimestamp(reply.ComposeTime)
			sender := reply.ImDisplayName
			if sender == "" {
				sender = "unknown"
			}
			reactions := formatReactions(*reply)
			fmt.Printf("  > [%s] %s: %s%s\n", ts, sender, content, reactions)
		}
	}
}

func filterMessagesSince(messages []api.ChatMessage, since string) []api.ChatMessage {
	// Parse the since date
	sinceTime, err := time.Parse("2006-01-02", since)
	if err != nil {
		// Try other formats
		sinceTime, err = time.Parse("2006-01-02T15:04:05", since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not parse --since date %q, showing all messages\n", since)
			return messages
		}
	}

	var filtered []api.ChatMessage
	for _, msg := range messages {
		for _, f := range timeFormats {
			if t, err := time.Parse(f, msg.ComposeTime); err == nil {
				if t.After(sinceTime) || t.Equal(sinceTime) {
					filtered = append(filtered, msg)
				}
				break
			}
		}
	}
	return filtered
}

func outputJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- Files ---

func filesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files <team-search>",
		Short: "List files from a team's SharePoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")

			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			// Find team by name to get group ID
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			teamID, teamName, err := findTeamBySearch(client, args[0])
			if err != nil {
				return err
			}

			graphClient := api.NewGraphClient(tokens.Graph)

			drive, _ := cmd.Flags().GetString("drive")
			allDrives, _ := cmd.Flags().GetBool("all-drives")

			if allDrives {
				// Show files from ALL drives
				fmt.Fprintf(os.Stderr, "fetching all drives from %s...\n", teamName)
				driveFiles, err := graphClient.GetTeamAllFiles(teamID)
				if err != nil {
					return fmt.Errorf("fetching drives: %w", err)
				}

				if jsonOutput {
					return outputJSON(driveFiles)
				}

				for driveName, files := range driveFiles {
					fmt.Printf("\n== %s ==\n", driveName)
					printFileList(files)
				}
				return nil
			}

			var files []api.DriveItem
			var fetchErr error
			if drive != "" {
				// Find specific drive by name
				drives, err := graphClient.GetTeamDrives(teamID)
				if err != nil {
					return fmt.Errorf("fetching drives: %w", err)
				}

				var driveID string
				for _, d := range drives {
					if strings.Contains(strings.ToLower(d.Name), strings.ToLower(drive)) {
						driveID = d.ID
						fmt.Fprintf(os.Stderr, "fetching files from %s / %s...\n", teamName, d.Name)
						break
					}
				}

				if driveID == "" {
					fmt.Fprintf(os.Stderr, "drive %q not found. available drives:\n", drive)
					for _, d := range drives {
						fmt.Fprintf(os.Stderr, "  - %s\n", d.Name)
					}
					return nil
				}

				if path != "" {
					files, fetchErr = graphClient.GetDriveFilesByPath(driveID, path)
				} else {
					files, fetchErr = graphClient.GetDriveFiles(driveID)
				}
			} else if path != "" {
				fmt.Fprintf(os.Stderr, "fetching files from %s/%s...\n", teamName, path)
				files, fetchErr = graphClient.GetTeamFilesByPath(teamID, path)
			} else {
				fmt.Fprintf(os.Stderr, "fetching files from %s...\n", teamName)
				files, fetchErr = graphClient.GetTeamFiles(teamID)
			}
			if fetchErr != nil {
				return fmt.Errorf("fetching files: %w", fetchErr)
			}

			if jsonOutput {
				return outputJSON(files)
			}

			if len(files) == 0 {
				fmt.Println("no files found")
				return nil
			}

			printFileList(files)
			return nil
		},
	}
	cmd.Flags().String("path", "", "subfolder path to list (e.g. 'General' or 'General/Homework')")
	cmd.Flags().String("drive", "", "specific drive name (e.g. 'Class Materials')")
	cmd.Flags().Bool("all-drives", false, "show files from all drives (Documents, Class Materials, etc)")
	return cmd
}

func printFileList(files []api.DriveItem) {
	for _, f := range files {
		icon := "F"
		sizeStr := fmt.Sprintf("%d KB", f.Size/1024)
		if f.Folder != nil {
			icon = "D"
			sizeStr = fmt.Sprintf("%d items", f.Folder.ChildCount)
		}

		modified := formatTimestamp(f.LastModifiedDateTime)
		modifiedBy := ""
		if f.LastModifiedBy != nil && f.LastModifiedBy.User != nil {
			modifiedBy = f.LastModifiedBy.User.DisplayName
		}

		fmt.Printf("%s %-45s  %10s  %s  %s\n", icon, f.Name, sizeStr, modified, modifiedBy)
	}
}

// --- Calendar ---

func calendarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "calendar",
		Short: "Show upcoming calendar events",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			days, _ := cmd.Flags().GetInt("days")

			graphClient := api.NewGraphClient(tokens.Graph)
			events, err := graphClient.GetCalendarEvents(days)
			if err != nil {
				return fmt.Errorf("fetching calendar: %w", err)
			}

			if jsonOutput {
				return outputJSON(events)
			}

			if len(events) == 0 {
				fmt.Println("no upcoming events")
				return nil
			}

			for _, e := range events {
				if e.IsCancelled {
					continue
				}

				start := ""
				if e.Start != nil {
					start = formatTimestamp(e.Start.DateTime)
				}

				end := ""
				if e.End != nil {
					end = formatTimestamp(e.End.DateTime)
				}

				location := ""
				if e.Location != nil && e.Location.DisplayName != "" {
					location = " @ " + e.Location.DisplayName
				}

				organizer := ""
				if e.Organizer != nil && e.Organizer.EmailAddress != nil {
					organizer = e.Organizer.EmailAddress.Name
				}

				fmt.Printf("[%s - %s] %s%s\n", start, end, e.Subject, location)
				if organizer != "" {
					fmt.Printf("  Organizer: %s\n", organizer)
				}
				if e.BodyPreview != "" {
					preview := e.BodyPreview
					if len(preview) > 100 {
						preview = preview[:100] + "..."
					}
					fmt.Printf("  %s\n", preview)
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().Int("days", 7, "number of days ahead to show")
	return cmd
}

// --- Assignments ---

func assignmentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assignments",
		Short: "View assignments from all classes",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokens, err := auth.Load()
			if err != nil {
				return err
			}

			// Auto-refresh if expired
			tokens, err = auth.EnsureFresh(tokens)
			if err != nil {
				return err
			}

			if tokens.Assignments == "" && tokens.Graph == "" {
				return fmt.Errorf("no assignments or graph token found - run 'exo-teams auth' to login")
			}

			graphClient := api.NewGraphClientWithAssignments(tokens.Graph, tokens.Assignments)

			listClasses, _ := cmd.Flags().GetBool("classes")
			if listClasses {
				classes, err := graphClient.GetClasses()
				if err != nil {
					return fmt.Errorf("fetching classes: %w", err)
				}

				if jsonOutput {
					return outputJSON(classes)
				}

				for _, c := range classes {
					fmt.Printf("%-60s %s\n", c.GetDisplayName(), c.ID)
				}
				return nil
			}

			fmt.Fprintln(os.Stderr, "fetching assignments with submission status...")
			assignments, err := graphClient.GetAllAssignmentsWithStatus()
			if err != nil {
				return fmt.Errorf("fetching assignments: %w", err)
			}

			if jsonOutput {
				return outputJSON(assignments)
			}

			if len(assignments) == 0 {
				fmt.Println("no assignments found")
				return nil
			}

			for _, a := range assignments {
				due := formatTimestamp(a.DueDateTime)
				name := a.DisplayName
				class := a.ClassName

				statusIcon := "[ ]"
				statusText := ""
				switch a.SubmissionStatus {
				case "submitted":
					statusIcon = "[x]"
					statusText = " (submitted)"
				case "returned":
					statusIcon = "[>]"
					statusText = " (returned)"
				case "working":
					statusIcon = "[~]"
					statusText = " (in progress)"
				}

				fmt.Printf("%s %-30s  due: %-20s  class: %s%s\n", statusIcon, name, due, class, statusText)

				if a.Instructions != nil && a.Instructions.Content != "" {
					content := stripHTML(a.Instructions.Content)
					if len(content) > 100 {
						content = content[:100] + "..."
					}
					fmt.Printf("  %s\n", content)
				}

				// Show submitted resources
				for _, r := range a.SubmittedResources {
					link := r.Resource.Link
					if link == "" {
						link = r.Resource.FileURL
					}
					if link != "" {
						fmt.Printf("  -> %s: %s\n", r.Resource.DisplayName, link)
					} else if r.Resource.DisplayName != "" {
						fmt.Printf("  -> %s\n", r.Resource.DisplayName)
					}
				}
			}

			return nil
		},
	}
	cmd.Flags().Bool("classes", false, "list education classes instead of assignments")
	return cmd
}

// --- Submit ---

func submitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit <class-search> <assignment-search>",
		Short: "Submit a file to an assignment",
		Long:  "Find a class and assignment by search term, then upload and submit a file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			classSearch := strings.ToLower(args[0])
			assignmentSearch := strings.ToLower(args[1])
			filePath, _ := cmd.Flags().GetString("file")

			if filePath == "" {
				return fmt.Errorf("--file is required")
			}

			tokens, err := auth.Load()
			if err != nil {
				return err
			}

			tokens, err = auth.EnsureFresh(tokens)
			if err != nil {
				return err
			}

			if tokens.Assignments == "" && tokens.Graph == "" {
				return fmt.Errorf("no assignments or graph token found - run 'exo-teams auth' to login")
			}

			graphClient := api.NewGraphClientWithAssignments(tokens.Graph, tokens.Assignments)

			// Find class
			classes, err := graphClient.GetClasses()
			if err != nil {
				return fmt.Errorf("fetching classes: %w", err)
			}

			var matchedClass *api.EducationClass
			for i := range classes {
				if strings.Contains(strings.ToLower(classes[i].GetDisplayName()), classSearch) {
					matchedClass = &classes[i]
					break
				}
			}

			if matchedClass == nil {
				return fmt.Errorf("no class found matching %q", args[0])
			}

			fmt.Fprintf(os.Stderr, "found class: %s\n", matchedClass.GetDisplayName())

			// Find assignment
			assignments, err := graphClient.GetAssignments(matchedClass.ID)
			if err != nil {
				return fmt.Errorf("fetching assignments: %w", err)
			}

			var matchedAssignment *api.Assignment
			for i := range assignments {
				if strings.Contains(strings.ToLower(assignments[i].DisplayName), assignmentSearch) {
					matchedAssignment = &assignments[i]
					break
				}
			}

			if matchedAssignment == nil {
				return fmt.Errorf("no assignment found matching %q", args[1])
			}

			fmt.Fprintf(os.Stderr, "found assignment: %s\n", matchedAssignment.DisplayName)

			// Get submission
			submission, err := graphClient.GetSubmission(matchedClass.ID, matchedAssignment.ID)
			if err != nil {
				return fmt.Errorf("getting submission: %w", err)
			}

			fmt.Fprintf(os.Stderr, "submission ID: %s (status: %s)\n", submission.ID, submission.Status)

			// Setup resources folder
			fmt.Fprintln(os.Stderr, "setting up resources folder...")
			if err := graphClient.SetupSubmissionResourcesFolder(matchedClass.ID, matchedAssignment.ID, submission.ID); err != nil {
				// Non-fatal - folder might already exist
				fmt.Fprintf(os.Stderr, "warning: setup resources folder: %v\n", err)
			}

			// Read file
			fileData, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", filePath, err)
			}

			fileName := filePath
			if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
				fileName = filePath[idx+1:]
			}

			// Upload resource
			fmt.Fprintf(os.Stderr, "uploading %s (%d KB)...\n", fileName, len(fileData)/1024)
			_, err = graphClient.UploadSubmissionResource(matchedClass.ID, matchedAssignment.ID, submission.ID, fileName, fileData)
			if err != nil {
				return fmt.Errorf("uploading resource: %w", err)
			}

			// Submit
			fmt.Fprintln(os.Stderr, "submitting assignment...")
			if err := graphClient.SubmitAssignment(matchedClass.ID, matchedAssignment.ID, submission.ID); err != nil {
				return fmt.Errorf("submitting: %w", err)
			}

			fmt.Fprintln(os.Stderr, "assignment submitted successfully")
			return nil
		},
	}
	cmd.Flags().String("file", "", "path to file to submit")
	return cmd
}

// --- Upload ---

func uploadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload <team-search>",
		Short: "Upload a file to a team's SharePoint",
		Long:  "Upload a local file to a team's drive at the specified remote path.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			remotePath, _ := cmd.Flags().GetString("path")
			localFile, _ := cmd.Flags().GetString("file")

			if remotePath == "" {
				return fmt.Errorf("--path is required (e.g. --path 'General/myfile.docx')")
			}
			if localFile == "" {
				return fmt.Errorf("--file is required (path to local file)")
			}

			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			// Find team
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			teamID, teamName, err := findTeamBySearch(client, args[0])
			if err != nil {
				return err
			}

			// Read file
			fileData, err := os.ReadFile(localFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", localFile, err)
			}

			graphClient := api.NewGraphClient(tokens.Graph)

			fmt.Fprintf(os.Stderr, "uploading to %s/%s (%d KB)...\n", teamName, remotePath, len(fileData)/1024)
			item, err := graphClient.UploadTeamFile(teamID, remotePath, fileData)
			if err != nil {
				return fmt.Errorf("uploading: %w", err)
			}

			fmt.Fprintf(os.Stderr, "uploaded: %s (%d KB)\n", item.Name, item.Size/1024)
			if item.WebURL != "" {
				fmt.Fprintf(os.Stderr, "url: %s\n", item.WebURL)
			}
			return nil
		},
	}
	cmd.Flags().String("path", "", "remote path within the team drive (e.g. 'General/report.pdf')")
	cmd.Flags().String("file", "", "local file to upload")
	return cmd
}

// --- Mark Read ---

func markReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mark-read <conversation-id>",
		Short: "Mark a conversation as read",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			if err := client.MarkConversationRead(args[0]); err != nil {
				return fmt.Errorf("marking as read: %w", err)
			}

			fmt.Fprintln(os.Stderr, "conversation marked as read")
			return nil
		},
	}
}

// --- Whoami ---

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show account info and token status",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokens, err := auth.Load()
			if err != nil {
				return fmt.Errorf("loading tokens: %w", err)
			}

			type tokenStatus struct {
				Name   string `json:"name"`
				Valid  bool   `json:"valid"`
				Expiry string `json:"expiry"`
			}

			tokenList := []struct {
				name  string
				token string
			}{
				{"skype", tokens.Skype},
				{"chatsvcagg", tokens.ChatSvcAgg},
				{"teams", tokens.Teams},
				{"graph", tokens.Graph},
				{"assignments", tokens.Assignments},
			}

			var statuses []tokenStatus
			for _, t := range tokenList {
				if t.token == "" {
					statuses = append(statuses, tokenStatus{t.name, false, "not set"})
					continue
				}

				exp, err := auth.GetTokenExpiry(t.token)
				if err != nil {
					statuses = append(statuses, tokenStatus{t.name, false, "invalid"})
					continue
				}

				valid := time.Now().Before(exp)
				statuses = append(statuses, tokenStatus{t.name, valid, exp.Local().Format("2006-01-02 15:04:05")})
			}

			if jsonOutput {
				return outputJSON(statuses)
			}

			// Try to get user info from Graph API
			if tokens.Graph != "" {
				req, err := http.NewRequest("GET", "https://graph.microsoft.com/v1.0/me", nil)
				if err == nil {
					req.Header.Set("Authorization", "Bearer "+tokens.Graph)
					httpClient := &http.Client{Timeout: 10 * time.Second}
					resp, err := httpClient.Do(req)
					if err == nil {
						defer resp.Body.Close()
						body, _ := io.ReadAll(resp.Body)
						var me struct {
							DisplayName string `json:"displayName"`
							Mail        string `json:"mail"`
							UPN         string `json:"userPrincipalName"`
						}
						if json.Unmarshal(body, &me) == nil && me.DisplayName != "" {
							fmt.Printf("user: %s\n", me.DisplayName)
							email := me.Mail
							if email == "" {
								email = me.UPN
							}
							if email != "" {
								fmt.Printf("email: %s\n", email)
							}
							fmt.Println()
						}
					}
				}
			}

			fmt.Println("tokens:")
			for _, s := range statuses {
				icon := "[x]"
				if !s.Valid {
					icon = "[ ]"
				}
				fmt.Printf("  %s %-15s  expires: %s\n", icon, s.Name, s.Expiry)
			}
			return nil
		},
	}
}

// --- Unread ---

func unreadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unread",
		Short: "Show unread messages across all chats",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			chats, err := client.GetChats()
			if err != nil {
				return fmt.Errorf("fetching chats: %w", err)
			}

			client.ResolveChatNames(chats)

			var unreadChats []api.Chat
			for _, chat := range chats {
				if r, ok := chat.IsRead.(bool); ok && !r {
					unreadChats = append(unreadChats, chat)
				}
			}

			if jsonOutput {
				return outputJSON(unreadChats)
			}

			if len(unreadChats) == 0 {
				fmt.Println("no unread messages")
				return nil
			}

			fmt.Printf("%d unread conversations:\n\n", len(unreadChats))

			for _, chat := range unreadChats {
				title := chat.Title
				if title == "" {
					title = formatMemberNames(chat.Members)
				}

				lastMsg := ""
				sender := ""
				if chat.LastMessage != nil {
					lastMsg = stripHTML(chat.LastMessage.Content)
					if len(lastMsg) > 80 {
						lastMsg = lastMsg[:80] + "..."
					}
					sender = chat.LastMessage.ImDisplayName
				}

				fmt.Printf("* %-40s %s\n", title, chat.ID)
				if sender != "" && lastMsg != "" {
					fmt.Printf("  %s: %s\n", sender, lastMsg)
				} else if lastMsg != "" {
					fmt.Printf("  %s\n", lastMsg)
				}
			}

			return nil
		},
	}
}

// --- Deadlines ---

func deadlinesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deadlines",
		Short: "Show all upcoming assignment deadlines sorted by date",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokens, err := auth.Load()
			if err != nil {
				return err
			}

			tokens, err = auth.EnsureFresh(tokens)
			if err != nil {
				return err
			}

			if tokens.Assignments == "" && tokens.Graph == "" {
				return fmt.Errorf("no assignments or graph token found - run 'exo-teams auth'")
			}

			graphClient := api.NewGraphClientWithAssignments(tokens.Graph, tokens.Assignments)

			fmt.Fprintln(os.Stderr, "fetching assignments...")
			assignments, err := graphClient.GetAllAssignmentsWithStatus()
			if err != nil {
				return fmt.Errorf("fetching assignments: %w", err)
			}

			// Filter to only pending (not submitted) and sort by due date
			var pending []api.AssignmentWithSubmission
			for _, a := range assignments {
				if a.SubmissionStatus != "submitted" && a.DueDateTime != "" {
					pending = append(pending, a)
				}
			}

			// Sort by due date
			sort.Slice(pending, func(i, j int) bool {
				return pending[i].DueDateTime < pending[j].DueDateTime
			})

			if jsonOutput {
				return outputJSON(pending)
			}

			if len(pending) == 0 {
				fmt.Println("no pending deadlines")
				return nil
			}

			now := time.Now()
			for _, a := range pending {
				due := formatTimestamp(a.DueDateTime)

				// Calculate days until due
				daysLeft := ""
				for _, f := range timeFormats {
					if t, err := time.Parse(f, a.DueDateTime); err == nil {
						d := int(t.Sub(now).Hours() / 24)
						if d < 0 {
							daysLeft = fmt.Sprintf(" (OVERDUE %d days)", -d)
						} else if d == 0 {
							daysLeft = " (TODAY)"
						} else if d == 1 {
							daysLeft = " (TOMORROW)"
						} else {
							daysLeft = fmt.Sprintf(" (%d days)", d)
						}
						break
					}
				}

				statusIcon := "[ ]"
				switch a.SubmissionStatus {
				case "working":
					statusIcon = "[~]"
				case "returned":
					statusIcon = "[>]"
				}

				fmt.Printf("%s %s  %-35s  %s%s\n", statusIcon, due, a.DisplayName, a.ClassName, daysLeft)
			}

			return nil
		},
	}
}
