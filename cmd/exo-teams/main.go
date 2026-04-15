package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// stop is invoked explicitly after ExecuteContext so os.Exit does not skip it

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

	err := rootCmd.ExecuteContext(ctx)
	stop()
	if err != nil {
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
func findTeamBySearch(ctx context.Context, client *api.Client, search string) (groupID, teamName string, err error) {
	teams, err := client.GetTeams(ctx)
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
			_ = cmd.Context()
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
			ctx := cmd.Context()
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			teams, err := client.GetTeams(ctx)
			if err != nil {
				return fmt.Errorf("fetching teams: %w", err)
			}

			if jsonOutput {
				return emitJSON(teams)
			}

			fmt.Print(renderTeamsTable(teams))
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
			ctx := cmd.Context()
			client, err := api.NewClientWithoutExchange()
			if err != nil {
				return err
			}

			chats, err := client.GetChats(ctx)
			if err != nil {
				return fmt.Errorf("fetching chats: %w", err)
			}

			// Resolve DM names
			client.ResolveChatNames(ctx, chats)

			if jsonOutput {
				return emitJSON(chats)
			}

			fmt.Print(renderChatsTable(chats))
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
			ctx := cmd.Context()
			search := strings.ToLower(args[0])
			count, _ := cmd.Flags().GetInt("count")
			allPages, _ := cmd.Flags().GetBool("all")
			showReplies, _ := cmd.Flags().GetBool("replies")

			client, err := api.NewClient()
			if err != nil {
				return err
			}

			teams, err := client.GetTeams(ctx)
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
				messages, err = client.GetAllMessages(ctx, matchedChannel.ID, 200, 0)
			} else {
				messages, err = client.GetAllMessages(ctx, matchedChannel.ID, count, 1)
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
				return emitJSON(messages)
			}

			fmt.Print(renderMessages(messages, showReplies))
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
			ctx := cmd.Context()
			search := strings.ToLower(args[0])
			count, _ := cmd.Flags().GetInt("count")
			allPages, _ := cmd.Flags().GetBool("all")
			showReplies, _ := cmd.Flags().GetBool("replies")

			client, err := api.NewClient()
			if err != nil {
				return err
			}

			chats, err := client.GetChats(ctx)
			if err != nil {
				return fmt.Errorf("fetching chats: %w", err)
			}

			// Resolve names for searching
			client.ResolveChatNames(ctx, chats)

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
				messages, err = client.GetAllMessages(ctx, matchedChat.ID, 200, 0)
			} else {
				messages, err = client.GetAllMessages(ctx, matchedChat.ID, count, 1)
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
				return emitJSON(messages)
			}

			fmt.Print(renderMessages(messages, showReplies))
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
			ctx := cmd.Context()
			client, err := api.NewClient()
			if err != nil {
				return err
			}
			if err := client.SendMessage(ctx, args[0], args[1]); err != nil {
				return err
			}
			statusf("message sent")
			if jsonOutput {
				return emitOK("")
			}
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
			ctx := cmd.Context()
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

				statusf("uploading %s (%d KB)...", filePath, len(fileData)/1024)

				msg := ""
				if i == 0 {
					msg = message
				}

				if err := client.SendMessageWithFile(ctx, args[0], msg, filePath, fileData); err != nil {
					return fmt.Errorf("sending file %s: %w", filePath, err)
				}

				statusf("file sent (%d/%d)", i+1, len(files))
			}

			if jsonOutput {
				return emitOK("")
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
			ctx := cmd.Context()
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

			statusf("found user: %s (%s)", matchedUser.DisplayName, matchedUser.Mail)

			// Create DM
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			mri := "8:orgid:" + matchedUser.ID
			statusf("creating conversation...")
			convID, err := client.StartNewDM(ctx, mri)
			if err != nil {
				return fmt.Errorf("creating DM: %w", err)
			}

			statusf("conversation created: %s", convID)

			// Send message
			if err := client.SendMessage(ctx, convID, message); err != nil {
				return fmt.Errorf("sending message: %w", err)
			}

			statusf("message sent")
			if jsonOutput {
				return emitOK(convID)
			}
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
			ctx := cmd.Context()
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

			teamID, teamName, err := findTeamBySearch(ctx, client, args[0])
			if err != nil {
				return err
			}

			graphClient := api.NewGraphClient(tokens.Graph)

			var item *api.DriveItem

			if driveName != "" {
				// Find specific drive and download from it
				drives, driveErr := graphClient.GetTeamDrives(ctx, teamID)
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
				item, err = graphClient.GetDriveFileByPath(ctx, driveID, filePath)
				if err != nil {
					return fmt.Errorf("getting file: %w", err)
				}
			} else {
				fmt.Fprintf(os.Stderr, "fetching file info from %s/%s...\n", teamName, filePath)
				item, err = graphClient.GetTeamFileByPath(ctx, teamID, filePath)
				if err != nil {
					return fmt.Errorf("getting file: %w", err)
				}
			}

			if item.DownloadURL == "" {
				return fmt.Errorf("no download URL available for this item (might be a folder)")
			}

			fmt.Fprintf(os.Stderr, "downloading %s (%d KB)...\n", item.Name, item.Size/1024)
			data, err := graphClient.DownloadFile(ctx, item.DownloadURL)
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

			statusf("saved to %s", outPath)
			if jsonOutput {
				return emitJSON(map[string]any{"ok": true, "path": outPath, "size": len(data)})
			}
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
			ctx := cmd.Context()
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			statusf("fetching activity feed...")

			items, err := client.GetActivity(ctx, 30)
			if err != nil {
				return fmt.Errorf("fetching activity: %w", err)
			}

			if jsonOutput {
				return emitJSON(items)
			}

			fmt.Print(renderActivity(items))
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
			ctx := cmd.Context()
			query := args[0]

			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			statusf("searching for %q...", query)

			graphClient := api.NewGraphClient(tokens.Graph)
			hits, err := graphClient.SearchMessages(ctx, query)
			if err != nil {
				return fmt.Errorf("searching: %w", err)
			}

			if jsonOutput {
				return emitJSON(hits)
			}

			fmt.Print(renderSearchHits(hits))
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

// --- Files ---

func filesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files <team-search>",
		Short: "List files from a team's SharePoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
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

			teamID, teamName, err := findTeamBySearch(ctx, client, args[0])
			if err != nil {
				return err
			}

			graphClient := api.NewGraphClient(tokens.Graph)

			drive, _ := cmd.Flags().GetString("drive")
			allDrives, _ := cmd.Flags().GetBool("all-drives")

			if allDrives {
				// Show files from ALL drives
				statusf("fetching all drives from %s...", teamName)
				driveFiles, err := graphClient.GetTeamAllFiles(ctx, teamID)
				if err != nil {
					return fmt.Errorf("fetching drives: %w", err)
				}

				if jsonOutput {
					return emitJSON(driveFiles)
				}

				fmt.Print(renderFilesByDrive(driveFiles))
				return nil
			}

			var files []api.DriveItem
			var fetchErr error
			if drive != "" {
				// Find specific drive by name
				drives, err := graphClient.GetTeamDrives(ctx, teamID)
				if err != nil {
					return fmt.Errorf("fetching drives: %w", err)
				}

				var driveID string
				for _, d := range drives {
					if strings.Contains(strings.ToLower(d.Name), strings.ToLower(drive)) {
						driveID = d.ID
						statusf("fetching files from %s / %s...", teamName, d.Name)
						break
					}
				}

				if driveID == "" {
					statusf("drive %q not found. available drives:", drive)
					for _, d := range drives {
						statusf("  - %s", d.Name)
					}
					return nil
				}

				if path != "" {
					files, fetchErr = graphClient.GetDriveFilesByPath(ctx, driveID, path)
				} else {
					files, fetchErr = graphClient.GetDriveFiles(ctx, driveID)
				}
			} else if path != "" {
				statusf("fetching files from %s/%s...", teamName, path)
				files, fetchErr = graphClient.GetTeamFilesByPath(ctx, teamID, path)
			} else {
				statusf("fetching files from %s...", teamName)
				files, fetchErr = graphClient.GetTeamFiles(ctx, teamID)
			}
			if fetchErr != nil {
				return fmt.Errorf("fetching files: %w", fetchErr)
			}

			if jsonOutput {
				return emitJSON(files)
			}

			fmt.Print(renderFilesTable(files))
			return nil
		},
	}
	cmd.Flags().String("path", "", "subfolder path to list (e.g. 'General' or 'General/Homework')")
	cmd.Flags().String("drive", "", "specific drive name (e.g. 'Class Materials')")
	cmd.Flags().Bool("all-drives", false, "show files from all drives (Documents, Class Materials, etc)")
	return cmd
}

// --- Calendar ---

func calendarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "calendar",
		Short: "Show upcoming calendar events",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tokens, err := loadGraphToken()
			if err != nil {
				return err
			}

			days, _ := cmd.Flags().GetInt("days")

			graphClient := api.NewGraphClient(tokens.Graph)
			events, err := graphClient.GetCalendarEvents(ctx, days)
			if err != nil {
				return fmt.Errorf("fetching calendar: %w", err)
			}

			if jsonOutput {
				return emitJSON(events)
			}

			fmt.Print(renderCalendar(events))
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
			ctx := cmd.Context()
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
				classes, err := graphClient.GetClasses(ctx, )
				if err != nil {
					return fmt.Errorf("fetching classes: %w", err)
				}

				if jsonOutput {
					return emitJSON(classes)
				}

				rows := make([][]string, 0, len(classes))
				for _, c := range classes {
					rows = append(rows, []string{c.GetDisplayName(), c.ID})
				}
				fmt.Print(mdTable([]string{"Class", "ID"}, rows))
				return nil
			}

			statusf("fetching assignments with submission status...")
			assignments, err := graphClient.GetAllAssignmentsWithStatus(ctx, )
			if err != nil {
				return fmt.Errorf("fetching assignments: %w", err)
			}

			if jsonOutput {
				return emitJSON(assignments)
			}

			fmt.Print(renderAssignments(assignments))
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
			ctx := cmd.Context()
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
			classes, err := graphClient.GetClasses(ctx, )
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

			statusf("found class: %s", matchedClass.GetDisplayName())

			// Find assignment
			assignments, err := graphClient.GetAssignments(ctx, matchedClass.ID)
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

			statusf("found assignment: %s", matchedAssignment.DisplayName)

			// Get submission
			submission, err := graphClient.GetSubmission(ctx, matchedClass.ID, matchedAssignment.ID)
			if err != nil {
				return fmt.Errorf("getting submission: %w", err)
			}

			statusf("submission ID: %s (status: %s)", submission.ID, submission.Status)

			// Setup resources folder
			statusf("setting up resources folder...")
			if err := graphClient.SetupSubmissionResourcesFolder(ctx, matchedClass.ID, matchedAssignment.ID, submission.ID); err != nil {
				// Non-fatal - folder might already exist
				statusf("warning: setup resources folder: %v", err)
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
			statusf("uploading %s (%d KB)...", fileName, len(fileData)/1024)
			_, err = graphClient.UploadSubmissionResource(ctx, matchedClass.ID, matchedAssignment.ID, submission.ID, fileName, fileData)
			if err != nil {
				return fmt.Errorf("uploading resource: %w", err)
			}

			// Submit
			statusf("submitting assignment...")
			if err := graphClient.SubmitAssignment(ctx, matchedClass.ID, matchedAssignment.ID, submission.ID); err != nil {
				return fmt.Errorf("submitting: %w", err)
			}

			statusf("assignment submitted successfully")
			if jsonOutput {
				return emitOK(submission.ID)
			}
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
			ctx := cmd.Context()
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

			teamID, teamName, err := findTeamBySearch(ctx, client, args[0])
			if err != nil {
				return err
			}

			// Read file
			fileData, err := os.ReadFile(localFile)
			if err != nil {
				return fmt.Errorf("reading file %s: %w", localFile, err)
			}

			graphClient := api.NewGraphClient(tokens.Graph)

			statusf("uploading to %s/%s (%d KB)...", teamName, remotePath, len(fileData)/1024)
			item, err := graphClient.UploadTeamFile(ctx, teamID, remotePath, fileData)
			if err != nil {
				return fmt.Errorf("uploading: %w", err)
			}

			statusf("uploaded: %s (%d KB)", item.Name, item.Size/1024)
			if item.WebURL != "" {
				statusf("url: %s", item.WebURL)
			}
			if jsonOutput {
				return emitJSON(map[string]any{"ok": true, "id": item.ID, "name": item.Name, "webUrl": item.WebURL})
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
			ctx := cmd.Context()
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			if err := client.MarkConversationRead(ctx, args[0]); err != nil {
				return fmt.Errorf("marking as read: %w", err)
			}

			statusf("conversation marked as read")
			if jsonOutput {
				return emitOK(args[0])
			}
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
			_ = cmd.Context()
			tokens, err := auth.Load()
			if err != nil {
				return fmt.Errorf("loading tokens: %w", err)
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

			statuses := make([]tokenStatusEntry, 0, len(tokenList))
			for _, t := range tokenList {
				if t.token == "" {
					statuses = append(statuses, tokenStatusEntry{Name: t.name, Valid: false, Expiry: "not set"})
					continue
				}

				exp, err := auth.GetTokenExpiry(t.token)
				if err != nil {
					statuses = append(statuses, tokenStatusEntry{Name: t.name, Valid: false, Expiry: "invalid"})
					continue
				}

				valid := time.Now().Before(exp)
				statuses = append(statuses, tokenStatusEntry{Name: t.name, Valid: valid, Expiry: exp.Local().Format("2006-01-02 15:04:05")})
			}

			// Try to get user info from Graph API
			info := whoamiInfo{Tokens: statuses}
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
							info.DisplayName = me.DisplayName
							info.Email = me.Mail
							if info.Email == "" {
								info.Email = me.UPN
							}
						}
					}
				}
			}

			if jsonOutput {
				return emitJSON(map[string]any{
					"user":   info.DisplayName,
					"email":  info.Email,
					"tokens": statuses,
				})
			}

			fmt.Print(renderWhoami(info))
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
			ctx := cmd.Context()
			client, err := api.NewClient()
			if err != nil {
				return err
			}

			chats, err := client.GetChats(ctx)
			if err != nil {
				return fmt.Errorf("fetching chats: %w", err)
			}

			client.ResolveChatNames(ctx, chats)

			var unreadChats []api.Chat
			for _, chat := range chats {
				if r, ok := chat.IsRead.(bool); ok && !r {
					unreadChats = append(unreadChats, chat)
				}
			}

			if jsonOutput {
				return emitJSON(unreadChats)
			}

			if len(unreadChats) == 0 {
				fmt.Print("_no unread messages_\n")
				return nil
			}

			fmt.Printf("## Unread (%d)\n\n", len(unreadChats))
			fmt.Print(renderChatsTable(unreadChats))
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
			ctx := cmd.Context()
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

			statusf("fetching assignments...")
			assignments, err := graphClient.GetAllAssignmentsWithStatus(ctx, )
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
				return emitJSON(pending)
			}

			fmt.Print(renderDeadlines(pending))
			return nil
		},
	}
}
