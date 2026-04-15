package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/alxxpersonal/exo-teams/internal/api"
)

// --- Output Helpers ---

// emitJSON writes v as indented JSON to stdout.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// emitOK writes a terse success object to stdout in JSON mode.
func emitOK(id string) error {
	payload := map[string]any{"ok": true}
	if id != "" {
		payload["id"] = id
	}
	return emitJSON(payload)
}

// statusf writes a status line to stderr with a trailing newline.
func statusf(format string, args ...any) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
}

// --- Markdown Helpers ---

// escapeCell escapes characters that would break a GitHub markdown table cell.
// Pipe characters are escaped, newlines are collapsed to spaces, backslashes
// are doubled so that the escape itself does not break rendering.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

// truncate returns s cut to max runes with an ellipsis marker if truncated.
func truncate(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "..."
}

// mdTable renders a GitHub-flavored markdown table from the given headers and rows.
// Cells are escaped. Empty rows return an empty string.
func mdTable(headers []string, rows [][]string) string {
	if len(headers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| ")
	for i, h := range headers {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(escapeCell(h))
	}
	b.WriteString(" |\n|")
	for range headers {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, row := range rows {
		b.WriteString("| ")
		for i := range headers {
			if i > 0 {
				b.WriteString(" | ")
			}
			if i < len(row) {
				b.WriteString(escapeCell(row[i]))
			} else {
				b.WriteString("-")
			}
		}
		b.WriteString(" |\n")
	}
	return b.String()
}

// --- Renderers ---

// renderTeamsTable renders a team list as a GitHub-flavored markdown table.
func renderTeamsTable(teams []api.Team) string {
	if len(teams) == 0 {
		return "_no teams found_\n"
	}
	rows := make([][]string, 0, len(teams))
	for _, t := range teams {
		name := t.DisplayName
		if t.IsArchived {
			name += " (archived)"
		}
		channels := make([]string, 0, len(t.Channels))
		for _, c := range t.Channels {
			label := c.DisplayName
			if c.IsGeneral {
				label += "*"
			}
			channels = append(channels, label)
		}
		channelList := strings.Join(channels, ", ")
		if channelList == "" {
			channelList = "-"
		}
		rows = append(rows, []string{name, channelList, t.TeamSiteInformation.GroupID})
	}
	return mdTable([]string{"Team", "Channels", "GroupID"}, rows)
}

// renderChatsTable renders a chat list as a GitHub-flavored markdown table.
func renderChatsTable(chats []api.Chat) string {
	if len(chats) == 0 {
		return "_no chats found_\n"
	}
	rows := make([][]string, 0, len(chats))
	for _, c := range chats {
		chatType := "group"
		if c.IsOneOnOne {
			chatType = "DM"
		}
		title := c.Title
		if title == "" {
			title = formatMemberNames(c.Members)
		}
		if title == "" {
			title = "(unnamed)"
		}
		last := "-"
		if c.LastMessage != nil && c.LastMessage.ComposeTime != "" {
			last = formatTimestamp(c.LastMessage.ComposeTime)
		}
		rows = append(rows, []string{chatType, title, c.ID, last})
	}
	return mdTable([]string{"Type", "Members", "ID", "Last Activity"}, rows)
}

// renderMessages renders a list of chat messages as markdown headings + body.
// Messages are rendered oldest first. System or control message types are skipped.
// If withReplies is true, replies are nested under their parent as blockquotes.
func renderMessages(messages []api.ChatMessage, withReplies bool) string {
	if withReplies {
		return renderMessagesThreaded(messages)
	}

	var b strings.Builder
	first := true
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !isRenderable(msg) {
			continue
		}
		content := stripHTML(msg.Content)
		if content == "" {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false
		writeMessageBlock(&b, msg, content, "")
	}
	return b.String()
}

// isRenderable returns true for message types that carry user content.
func isRenderable(m api.ChatMessage) bool {
	return m.MessageType == "RichText/Html" ||
		m.MessageType == "Text" ||
		m.MessageType == "RichText"
}

// writeMessageBlock writes a single message as a `### sender - ts` block with
// body and optional reactions line. prefix is prepended to every body line and
// is used to nest replies as blockquotes.
func writeMessageBlock(b *strings.Builder, msg api.ChatMessage, content, prefix string) {
	ts := formatTimestamp(msg.ComposeTime)
	sender := msg.ImDisplayName
	if sender == "" {
		sender = "unknown"
	}
	fmt.Fprintf(b, "%s### %s - %s\n", prefix, sender, ts)
	for _, line := range strings.Split(content, "\n") {
		fmt.Fprintf(b, "%s%s\n", prefix, line)
	}
	if r := renderReactions(msg); r != "" {
		fmt.Fprintf(b, "%s%s\n", prefix, r)
	}
}

// renderReactions returns a single-line reactions summary, or "" if none.
func renderReactions(msg api.ChatMessage) string {
	emotions := msg.GetEmotions()
	if len(emotions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(emotions))
	for k := range emotions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %s x%d", reactionIcon(k), k, emotions[k]))
	}
	return "reacts: " + strings.Join(parts, ", ")
}

// reactionIcon maps a reaction key to a display glyph. Returns the key when no
// mapping exists so the renderer stays resilient to new reaction types.
func reactionIcon(key string) string {
	switch strings.ToLower(key) {
	case "like", "thumbsup":
		return "[+1]"
	case "heart", "love":
		return "[heart]"
	case "laugh":
		return "[laugh]"
	case "surprised":
		return "[wow]"
	case "sad":
		return "[sad]"
	case "angry":
		return "[angry]"
	default:
		return "[" + key + "]"
	}
}

// renderMessagesThreaded groups messages into parent/reply threads and renders
// replies nested under each parent as blockquotes.
func renderMessagesThreaded(messages []api.ChatMessage) string {
	type thread struct {
		root    *api.ChatMessage
		replies []*api.ChatMessage
	}
	threadMap := make(map[string]*thread)
	msgByID := make(map[string]*api.ChatMessage)
	for i := range messages {
		msgByID[messages[i].ID] = &messages[i]
	}
	var topLevel []*thread
	for i := range messages {
		msg := &messages[i]
		if !isRenderable(*msg) {
			continue
		}
		parentID := msg.ParentMessageID()
		if parentID == "" {
			t := &thread{root: msg}
			threadMap[msg.ID] = t
			topLevel = append(topLevel, t)
			continue
		}
		if t, ok := threadMap[parentID]; ok {
			t.replies = append(t.replies, msg)
			continue
		}
		t := &thread{replies: []*api.ChatMessage{msg}}
		threadMap[parentID] = t
		if parent, ok := msgByID[parentID]; ok {
			t.root = parent
		}
		topLevel = append(topLevel, t)
	}

	var b strings.Builder
	first := true
	for i := len(topLevel) - 1; i >= 0; i-- {
		t := topLevel[i]
		if t.root != nil {
			content := stripHTML(t.root.Content)
			if content == "" && len(t.replies) == 0 {
				continue
			}
			if content != "" {
				if !first {
					b.WriteString("\n")
				}
				first = false
				writeMessageBlock(&b, *t.root, content, "")
			}
		}
		for j := len(t.replies) - 1; j >= 0; j-- {
			reply := t.replies[j]
			content := stripHTML(reply.Content)
			if content == "" {
				continue
			}
			if !first {
				b.WriteString("\n")
			}
			first = false
			writeMessageBlock(&b, *reply, content, "> ")
		}
	}
	return b.String()
}

// renderAssignments renders assignments grouped by class. Each class becomes an
// `## Class` section with a table of Status, Title, Due and Grade.
func renderAssignments(assignments []api.AssignmentWithSubmission) string {
	if len(assignments) == 0 {
		return "_no assignments found_\n"
	}
	groups := make(map[string][]api.AssignmentWithSubmission)
	order := make([]string, 0)
	for _, a := range assignments {
		class := a.ClassName
		if class == "" {
			class = "(unknown class)"
		}
		if _, ok := groups[class]; !ok {
			order = append(order, class)
		}
		groups[class] = append(groups[class], a)
	}
	sort.Strings(order)

	var b strings.Builder
	for i, class := range order {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## %s\n\n", class)
		rows := make([][]string, 0, len(groups[class]))
		for _, a := range groups[class] {
			rows = append(rows, []string{
				statusMarker(a.SubmissionStatus),
				a.DisplayName,
				formatTimestamp(a.DueDateTime),
				"-",
			})
		}
		b.WriteString(mdTable([]string{"Status", "Title", "Due", "Grade"}, rows))
	}
	return b.String()
}

// statusMarker returns the ASCII marker for a submission status.
func statusMarker(status string) string {
	switch status {
	case "submitted":
		return "[x]"
	case "returned":
		return "[>]"
	case "working":
		return "[~]"
	default:
		return "[ ]"
	}
}

// renderDeadlines renders pending assignments as a flat table sorted by due date.
func renderDeadlines(pending []api.AssignmentWithSubmission) string {
	if len(pending) == 0 {
		return "_no pending deadlines_\n"
	}
	rows := make([][]string, 0, len(pending))
	for _, a := range pending {
		rows = append(rows, []string{
			formatTimestamp(a.DueDateTime),
			a.ClassName,
			a.DisplayName,
			statusMarker(a.SubmissionStatus),
		})
	}
	return mdTable([]string{"Due", "Class", "Title", "Status"}, rows)
}

// renderCalendar renders calendar events as per-event sections with bullets.
func renderCalendar(events []api.CalendarEvent) string {
	if len(events) == 0 {
		return "_no upcoming events_\n"
	}
	var b strings.Builder
	first := true
	for _, e := range events {
		if e.IsCancelled {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false
		fmt.Fprintf(&b, "### %s\n", e.Subject)

		when := ""
		if e.Start != nil {
			when = formatTimestamp(e.Start.DateTime)
		}
		if e.End != nil {
			when += " - " + formatTimestamp(e.End.DateTime)
		}
		if when != "" {
			fmt.Fprintf(&b, "- when: %s\n", when)
		}

		if e.Location != nil && e.Location.DisplayName != "" {
			fmt.Fprintf(&b, "- where: %s\n", e.Location.DisplayName)
		}
		if e.Organizer != nil && e.Organizer.EmailAddress != nil && e.Organizer.EmailAddress.Name != "" {
			fmt.Fprintf(&b, "- organizer: %s\n", e.Organizer.EmailAddress.Name)
		}
		if e.BodyPreview != "" {
			preview := truncate(e.BodyPreview, 200)
			fmt.Fprintf(&b, "- preview: %s\n", preview)
		}
	}
	return b.String()
}

// renderFilesTable renders a single drive listing as a markdown table.
func renderFilesTable(files []api.DriveItem) string {
	if len(files) == 0 {
		return "_no files found_\n"
	}
	rows := make([][]string, 0, len(files))
	for _, f := range files {
		kind := "file"
		size := fmt.Sprintf("%d KB", f.Size/1024)
		if f.Folder != nil {
			kind = "folder"
			size = fmt.Sprintf("%d items", f.Folder.ChildCount)
		}
		path := ""
		if f.ParentReference != nil {
			path = f.ParentReference.Path
		}
		rows = append(rows, []string{
			f.Name,
			kind,
			size,
			formatTimestamp(f.LastModifiedDateTime),
			path,
		})
	}
	return mdTable([]string{"Name", "Type", "Size", "Modified", "Path"}, rows)
}

// renderFilesByDrive renders multiple drives as `## Drive` sections with tables.
func renderFilesByDrive(driveFiles map[string][]api.DriveItem) string {
	if len(driveFiles) == 0 {
		return "_no files found_\n"
	}
	drives := make([]string, 0, len(driveFiles))
	for d := range driveFiles {
		drives = append(drives, d)
	}
	sort.Strings(drives)
	var b strings.Builder
	for i, d := range drives {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## %s\n\n", d)
		b.WriteString(renderFilesTable(driveFiles[d]))
	}
	return b.String()
}

// renderActivity renders the activity feed as a bulleted markdown list.
func renderActivity(items []api.ActivityMessage) string {
	if len(items) == 0 {
		return "_no recent activity_\n"
	}
	var b strings.Builder
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
		summary := truncate(stripHTML(item.Content), 160)
		if summary == "" {
			summary = sender
		} else {
			summary = sender + ": " + summary
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", ts, actType, summary)
	}
	return b.String()
}

// renderSearchHits renders Graph search results as per-hit sections.
func renderSearchHits(hits []api.SearchHit) string {
	if len(hits) == 0 {
		return "_no results found_\n"
	}
	var b strings.Builder
	first := true
	for _, hit := range hits {
		title, snippet, link := extractSearchFields(hit)
		if !first {
			b.WriteString("\n")
		}
		first = false
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "### %s\n", title)
		if snippet != "" {
			fmt.Fprintf(&b, "%s\n", snippet)
		}
		if link != "" {
			fmt.Fprintf(&b, "link: %s\n", link)
		}
	}
	return b.String()
}

// extractSearchFields pulls title, snippet and link from a heterogeneous Graph
// search hit. Missing fields come back as empty strings.
func extractSearchFields(hit api.SearchHit) (title, snippet, link string) {
	snippet = stripHTML(hit.Summary)
	if hit.Resource == nil {
		return "", snippet, ""
	}
	if name, ok := hit.Resource["name"].(string); ok && name != "" {
		title = name
	}
	if subject, ok := hit.Resource["subject"].(string); ok && subject != "" && title == "" {
		title = subject
	}
	if ts, ok := hit.Resource["createdDateTime"].(string); ok && ts != "" {
		if title == "" {
			title = formatTimestamp(ts)
		}
	}
	if body, ok := hit.Resource["body"].(map[string]any); ok {
		if content, ok := body["content"].(string); ok && content != "" {
			snippet = truncate(stripHTML(content), 200)
		}
	}
	if preview, ok := hit.Resource["bodyPreview"].(string); ok && snippet == "" {
		snippet = truncate(stripHTML(preview), 200)
	}
	if url, ok := hit.Resource["webUrl"].(string); ok {
		link = url
	}
	return title, snippet, link
}

// renderWhoami renders account info and token statuses as a markdown section.
type whoamiInfo struct {
	DisplayName string
	Email       string
	Tokens      []tokenStatusEntry
}

type tokenStatusEntry struct {
	Name   string `json:"name"`
	Valid  bool   `json:"valid"`
	Expiry string `json:"expiry"`
}

// renderWhoami renders the whoami payload as a markdown section.
func renderWhoami(info whoamiInfo) string {
	var b strings.Builder
	b.WriteString("## Account\n\n")
	if info.DisplayName != "" {
		fmt.Fprintf(&b, "- user: %s\n", info.DisplayName)
	}
	if info.Email != "" {
		fmt.Fprintf(&b, "- email: %s\n", info.Email)
	}
	if info.DisplayName == "" && info.Email == "" {
		b.WriteString("- user: (unknown)\n")
	}
	b.WriteString("\n## Tokens\n\n")
	rows := make([][]string, 0, len(info.Tokens))
	for _, t := range info.Tokens {
		marker := "[ ]"
		if t.Valid {
			marker = "[x]"
		}
		rows = append(rows, []string{marker, t.Name, t.Expiry})
	}
	b.WriteString(mdTable([]string{"Valid", "Token", "Expires"}, rows))
	return b.String()
}


