package api

import (
	"testing"
)

// --- ParentMessageID ---

func TestParentMessageID(t *testing.T) {
	tests := []struct {
		name string
		msg  ChatMessage
		want string
	}{
		{"nil properties", ChatMessage{Properties: nil}, ""},
		{"no parent key", ChatMessage{Properties: map[string]any{"other": "val"}}, ""},
		{"non-string parent", ChatMessage{Properties: map[string]any{"parentMessageId": 123}}, ""},
		{"valid parent", ChatMessage{Properties: map[string]any{"parentMessageId": "12345"}}, "12345"},
		{"empty parent", ChatMessage{Properties: map[string]any{"parentMessageId": ""}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.msg.ParentMessageID()
			if got != tt.want {
				t.Errorf("ParentMessageID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- GetEmotions ---

func TestGetEmotions_ArrayForm(t *testing.T) {
	msg := ChatMessage{
		AnnotationsSummary: &AnnotationsSummary{
			Emotions: []any{
				map[string]any{"key": "like", "users_count": float64(3)},
				map[string]any{"key": "heart", "users_count": float64(1)},
			},
		},
	}

	emotions := msg.GetEmotions()

	if emotions["like"] != 3 {
		t.Errorf("like = %d, want 3", emotions["like"])
	}
	if emotions["heart"] != 1 {
		t.Errorf("heart = %d, want 1", emotions["heart"])
	}
}

func TestGetEmotions_MapForm(t *testing.T) {
	msg := ChatMessage{
		AnnotationsSummary: &AnnotationsSummary{
			Emotions: map[string]any{
				"like":  float64(5),
				"laugh": float64(2),
			},
		},
	}

	emotions := msg.GetEmotions()

	if emotions["like"] != 5 {
		t.Errorf("like = %d, want 5", emotions["like"])
	}
	if emotions["laugh"] != 2 {
		t.Errorf("laugh = %d, want 2", emotions["laugh"])
	}
}

func TestGetEmotions_NilSummary(t *testing.T) {
	msg := ChatMessage{}
	emotions := msg.GetEmotions()
	if len(emotions) != 0 {
		t.Errorf("expected empty map, got %v", emotions)
	}
}

func TestGetEmotions_PropertiesFallback(t *testing.T) {
	msg := ChatMessage{
		Properties: map[string]any{
			"emotions": map[string]any{
				"like": float64(7),
			},
		},
	}

	emotions := msg.GetEmotions()
	if emotions["like"] != 7 {
		t.Errorf("like = %d, want 7", emotions["like"])
	}
}

func TestGetEmotions_EmptyKey(t *testing.T) {
	msg := ChatMessage{
		AnnotationsSummary: &AnnotationsSummary{
			Emotions: []any{
				map[string]any{"key": "", "users_count": float64(1)},
			},
		},
	}

	emotions := msg.GetEmotions()
	if _, ok := emotions[""]; ok {
		t.Error("empty key should not be included")
	}
}

// --- GetDisplayName ---

func TestGetDisplayName(t *testing.T) {
	tests := []struct {
		name    string
		class   EducationClass
		want    string
	}{
		{"prefers DisplayName", EducationClass{DisplayName: "Math", Name: "MATH101"}, "Math"},
		{"falls back to Name", EducationClass{Name: "MATH101"}, "MATH101"},
		{"both empty", EducationClass{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.class.GetDisplayName()
			if got != tt.want {
				t.Errorf("GetDisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}
