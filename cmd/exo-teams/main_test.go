package main

import (
	"testing"
	"time"

	"github.com/alxxpersonal/exo-teams/internal/api"
)

// --- StripHTML ---

func TestStripHTML_BasicTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", ""},
		{"no html", "hello world", "hello world"},
		{"paragraph", "<p>hello</p>", "hello"},
		{"nested", "<div><p>hello</p></div>", "hello"},
		{"self-closing", "hello<br/>world", "helloworld"},
		{"attributes", `<a href="url">link</a>`, "link"},
		{"multiple tags", "<b>bold</b> and <i>italic</i>", "bold and italic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHTML_Entities(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"nbsp", "hello&nbsp;world", "hello world"},
		{"amp", "a&amp;b", "a&b"},
		{"lt gt", "&lt;tag&gt;", "<tag>"},
		{"quot", "&quot;quoted&quot;", `"quoted"`},
		{"apos", "it&#39;s", "it's"},
		{"mixed", "<p>a&nbsp;&amp;&nbsp;b</p>", "a & b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHTML_Whitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"collapses spaces", "hello   world", "hello world"},
		{"trims", "  hello  ", "hello"},
		{"newlines", "hello\n\nworld", "hello world"},
		{"tabs", "hello\t\tworld", "hello world"},
		{"whitespace only", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- FormatTimestamp ---

func TestFormatTimestamp_Formats(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool // if true, check it parses; if false, expect raw return
	}{
		{"rfc3339", "2026-03-18T19:00:00Z", true},
		{"rfc3339 nano", "2026-03-18T19:00:00.1234567Z", true},
		{"milliseconds", "2026-03-18T19:00:00.000Z", true},
		{"no timezone", "2026-03-18T19:00:00", true},
		{"teams format", "2026-03-18T19:00:00.0000000Z", true},
		{"garbage", "not-a-date", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimestamp(tt.input)
			if tt.valid {
				if got == tt.input {
					t.Errorf("formatTimestamp(%q) returned raw input, expected parsed format", tt.input)
				}
				// Should match YYYY-MM-DD HH:MM format
				_, err := time.Parse("2006-01-02 15:04", got)
				if err != nil {
					t.Errorf("formatTimestamp(%q) = %q, not in expected format: %v", tt.input, got, err)
				}
			} else {
				if got != tt.input {
					t.Errorf("formatTimestamp(%q) = %q, want raw input back", tt.input, got)
				}
			}
		})
	}
}

// --- FormatMemberNames ---

func TestFormatMemberNames(t *testing.T) {
	tests := []struct {
		name    string
		members []api.ChatMember
		want    string
	}{
		{"empty", nil, ""},
		{"one member", []api.ChatMember{{FriendlyName: "Alice"}}, "Alice"},
		{"two members", []api.ChatMember{{FriendlyName: "Alice"}, {FriendlyName: "Bob"}}, "Alice, Bob"},
		{"three members", []api.ChatMember{{FriendlyName: "Alice"}, {FriendlyName: "Bob"}, {FriendlyName: "Charlie"}}, "Alice, Bob, Charlie"},
		{"four members truncates", []api.ChatMember{{FriendlyName: "Alice"}, {FriendlyName: "Bob"}, {FriendlyName: "Charlie"}, {FriendlyName: "Dave"}}, "Alice, Bob +2 more"},
		{"empty names skipped", []api.ChatMember{{FriendlyName: ""}, {FriendlyName: "Bob"}}, "Bob"},
		{"all empty names", []api.ChatMember{{FriendlyName: ""}, {FriendlyName: ""}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMemberNames(tt.members)
			if got != tt.want {
				t.Errorf("formatMemberNames() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- FilterMessagesSince ---

func TestFilterMessagesSince(t *testing.T) {
	msgs := []api.ChatMessage{
		{ComposeTime: "2026-03-15T10:00:00.000Z", Content: "old"},
		{ComposeTime: "2026-03-17T10:00:00.000Z", Content: "middle"},
		{ComposeTime: "2026-03-19T10:00:00.000Z", Content: "new"},
	}

	tests := []struct {
		name  string
		since string
		count int
	}{
		{"all after early date", "2026-03-01", 3},
		{"two after mid date", "2026-03-16", 2},
		{"one after late date", "2026-03-18", 1},
		{"none after future date", "2026-04-01", 0},
		{"exact boundary included", "2026-03-17", 2},
		{"unparseable returns all", "garbage", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterMessagesSince(msgs, tt.since)
			if len(got) != tt.count {
				t.Errorf("filterMessagesSince(since=%q) returned %d messages, want %d", tt.since, len(got), tt.count)
			}
		})
	}
}

// --- RenderReactions ---

func TestRenderReactions(t *testing.T) {
	tests := []struct {
		name string
		msg  api.ChatMessage
		want string
	}{
		{
			"no annotations",
			api.ChatMessage{},
			"",
		},
		{
			"nil summary",
			api.ChatMessage{AnnotationsSummary: nil},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderReactions(tt.msg)
			if tt.want == "" && got != "" {
				t.Errorf("renderReactions() = %q, want empty", got)
			}
		})
	}
}
