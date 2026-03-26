package refinery

import (
	"testing"
)

func TestGetReviewRound(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        int
	}{
		{"empty description", "", 0},
		{"no review_round field", "convoy_status: open\nowner: crew/nuclear", 0},
		{"round 1", "review_round: 1\nconvoy_status: changes_requested", 1},
		{"round 3", "review_round: 3", 3},
		{"round 5", "review_round: 5", 5},
		{"non-numeric", "review_round: abc", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getReviewRound(tt.description)
			if got != tt.want {
				t.Errorf("getReviewRound() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShouldEscalate(t *testing.T) {
	tests := []struct {
		name  string
		round int
		want  bool
	}{
		{"round 1 does not escalate", 1, false},
		{"round 2 does not escalate", 2, false},
		{"round 3 does not escalate", 3, false},
		{"round 4 escalates", 4, true},
		{"round 5 escalates", 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.round > maxReviewRounds
			if got != tt.want {
				t.Errorf("round %d > maxReviewRounds = %v, want %v", tt.round, got, tt.want)
			}
		})
	}
}

func TestFormatReviewSummary(t *testing.T) {
	comments := []reviewComment{
		{User: "alice", Path: "main.go", Line: 42, Body: "Fix this nil check"},
		{User: "bob", Path: "util.go", Line: 10, Body: "Rename variable"},
	}

	summary := formatReviewSummary(comments)

	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	for _, want := range []string{"alice", "bob", "main.go", "util.go", "Fix this nil check", "Rename variable"} {
		if !contains(summary, want) {
			t.Errorf("summary missing %q", want)
		}
	}
}

func TestFormatReviewSummary_Empty(t *testing.T) {
	summary := formatReviewSummary(nil)
	if summary == "" {
		t.Fatal("expected fallback message for empty comments")
	}
}

func TestBuildEscalationMailBody(t *testing.T) {
	body := buildEscalationMailBody("gt-abc", 4, 123, "(no inline comments)")
	if body == "" {
		t.Fatal("expected non-empty mail body")
	}
	for _, want := range []string{"gt-abc", "4", "123", "no inline comments"} {
		if !contains(body, want) {
			t.Errorf("mail body missing %q", want)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
