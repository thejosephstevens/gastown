package refinery

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
)

const maxReviewRounds = 3

// reviewComment represents a single PR review comment.
type reviewComment struct {
	ID      int64
	User    string
	Path    string
	Line    int
	Body    string
	HTMLURL string
}

// getReviewRound extracts the review_round counter from a convoy description.
// Returns 0 if not set or not parseable.
func getReviewRound(description string) int {
	val := beads.GetReviewRoundField(description)
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return n
}

// formatReviewSummary formats review comments into a human-readable markdown summary.
func formatReviewSummary(comments []reviewComment) string {
	if len(comments) == 0 {
		return "(No inline comments found — check the PR for review-level feedback)"
	}
	var b strings.Builder
	for i, c := range comments {
		fmt.Fprintf(&b, "### Comment %d\n", i+1)
		fmt.Fprintf(&b, "- **Reviewer**: %s\n", c.User)
		fmt.Fprintf(&b, "- **File**: `%s` (line %d)\n", c.Path, c.Line)
		fmt.Fprintf(&b, "- **Comment**: %s\n\n", c.Body)
	}
	return b.String()
}

// buildEscalationMailBody constructs the mail body sent to crew when review
// rounds exceed the limit.
func buildEscalationMailBody(convoyID string, round, prNumber int, commentSummary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "REVIEW_ESCALATION: convoy=%s pr=#%d round=%d\n\n", convoyID, prNumber, round)
	fmt.Fprintf(&b, "Review round limit (%d) exceeded after %d rounds.\n", maxReviewRounds, round)
	b.WriteString("No more polecats will be dispatched. Manual intervention needed.\n\n")
	b.WriteString("Options:\n")
	b.WriteString("1. Dispatch a polecat manually with specific instructions\n")
	b.WriteString("2. Escalate to overseer for architectural guidance\n")
	b.WriteString("3. Address the review comments directly\n\n")
	b.WriteString("Outstanding review comments:\n")
	b.WriteString(commentSummary)
	return b.String()
}
