package main

import (
	"strings"
	"testing"

	"github.com/alxxpersonal/exo-teams/internal/api"
)

func TestMdTable(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		rows    [][]string
		want    []string
	}{
		{
			name:    "simple table",
			headers: []string{"A", "B"},
			rows:    [][]string{{"1", "2"}, {"3", "4"}},
			want:    []string{"| A | B |", "| --- | --- |", "| 1 | 2 |", "| 3 | 4 |"},
		},
		{
			name:    "escapes pipes in cells",
			headers: []string{"Name"},
			rows:    [][]string{{"a|b"}},
			want:    []string{`| a\|b |`},
		},
		{
			name:    "empty cell becomes dash",
			headers: []string{"X"},
			rows:    [][]string{{""}},
			want:    []string{"| - |"},
		},
		{
			name:    "missing cell filled with dash",
			headers: []string{"A", "B"},
			rows:    [][]string{{"only-a"}},
			want:    []string{"| only-a | - |"},
		},
		{
			name:    "empty headers returns empty string",
			headers: nil,
			rows:    [][]string{{"x"}},
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mdTable(tt.headers, tt.rows)
			if tt.want == nil {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			for _, line := range tt.want {
				if !strings.Contains(got, line) {
					t.Errorf("expected line %q in output, got:\n%s", line, got)
				}
			}
		})
	}
}

func TestRenderTeamsTable(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got := renderTeamsTable(nil)
		if !strings.Contains(got, "no teams found") {
			t.Errorf("expected empty marker, got %q", got)
		}
	})
	t.Run("renders archived marker and general channel", func(t *testing.T) {
		teams := []api.Team{{
			DisplayName: "Team A",
			IsArchived:  true,
			Channels: []api.Channel{
				{DisplayName: "general", IsGeneral: true},
				{DisplayName: "random"},
			},
			TeamSiteInformation: api.TeamSite{GroupID: "gid-1"},
		}}
		got := renderTeamsTable(teams)
		if !strings.Contains(got, "Team A (archived)") {
			t.Errorf("expected archived marker, got %q", got)
		}
		if !strings.Contains(got, "general*") {
			t.Errorf("expected general star marker, got %q", got)
		}
		if !strings.Contains(got, "gid-1") {
			t.Errorf("expected group id, got %q", got)
		}
	})
}

func TestRenderAssignments(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got := renderAssignments(nil)
		if !strings.Contains(got, "no assignments found") {
			t.Errorf("expected empty marker, got %q", got)
		}
	})
	t.Run("groups by class with H2 and table", func(t *testing.T) {
		assignments := []api.AssignmentWithSubmission{
			{Assignment: api.Assignment{DisplayName: "HW1", ClassName: "Math"}, SubmissionStatus: "submitted"},
			{Assignment: api.Assignment{DisplayName: "HW2", ClassName: "Math"}, SubmissionStatus: "working"},
			{Assignment: api.Assignment{DisplayName: "Lab1", ClassName: "Physics"}, SubmissionStatus: "not submitted"},
		}
		got := renderAssignments(assignments)
		if !strings.Contains(got, "## Math") {
			t.Errorf("expected Math section header, got:\n%s", got)
		}
		if !strings.Contains(got, "## Physics") {
			t.Errorf("expected Physics section header, got:\n%s", got)
		}
		if !strings.Contains(got, "[x]") || !strings.Contains(got, "[~]") || !strings.Contains(got, "[ ]") {
			t.Errorf("expected status markers, got:\n%s", got)
		}
	})
}

func TestRenderDeadlines(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got := renderDeadlines(nil)
		if !strings.Contains(got, "no pending deadlines") {
			t.Errorf("expected empty marker, got %q", got)
		}
	})
	t.Run("flat table with rows", func(t *testing.T) {
		pending := []api.AssignmentWithSubmission{
			{Assignment: api.Assignment{DisplayName: "HW1", ClassName: "Math"}, SubmissionStatus: "working"},
		}
		got := renderDeadlines(pending)
		if !strings.Contains(got, "| Due | Class | Title | Status |") {
			t.Errorf("expected deadlines header, got:\n%s", got)
		}
		if !strings.Contains(got, "HW1") || !strings.Contains(got, "Math") {
			t.Errorf("expected assignment row, got:\n%s", got)
		}
	})
}

func TestEscapeCell(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"with|pipe", `with\|pipe`},
		{"with\nnewline", "with newline"},
		{"", "-"},
		{"  spaced  ", "spaced"},
	}
	for _, tt := range tests {
		if got := escapeCell(tt.in); got != tt.want {
			t.Errorf("escapeCell(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("got %q, want %q", got, "hello...")
	}
	if got := truncate("anything", 0); got != "anything" {
		t.Error("zero max should return input unchanged")
	}
}
