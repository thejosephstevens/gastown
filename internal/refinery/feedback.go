package refinery

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/util"
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

// FeedbackResult holds the outcome of processing a CHANGES_REQUESTED review.
type FeedbackResult struct {
	ConvoyID   string
	PRNumber   int
	Round      int
	FeedbackID string
	Escalated  bool
	Error      string
}

// HandleReviewFeedback processes a convoy whose PR received CHANGES_REQUESTED.
// It increments the review round counter, fetches review comments, and either
// dispatches a polecat (round <= maxReviewRounds) or escalates to the crew via
// mail (round > maxReviewRounds), setting the convoy status to review_escalated.
func HandleReviewFeedback(ctx context.Context, hqBeads *beads.Beads, townRoot string, convoy *beads.Issue, prNumber int, owner, repo string, logger func(format string, args ...interface{}), gtPath string) *FeedbackResult {
	if logger == nil {
		logger = func(format string, args ...interface{}) {}
	}

	result := &FeedbackResult{
		ConvoyID: convoy.ID,
		PRNumber: prNumber,
	}

	// 1. Increment review round counter on the convoy bead.
	round := getReviewRound(convoy.Description) + 1
	result.Round = round

	newDesc := setMetadataFields(convoy.Description, map[string]string{
		"convoy_status":     "changes_requested",
		"review_round":      strconv.Itoa(round),
		"review_changes_at": time.Now().UTC().Format(time.RFC3339),
	})
	if err := hqBeads.Update(convoy.ID, beads.UpdateOptions{Description: &newDesc}); err != nil {
		logger("Feedback: convoy %s: failed to update round counter: %v", convoy.ID, err)
	}

	logger("Feedback: convoy %s PR #%d: round %d of %d", convoy.ID, prNumber, round, maxReviewRounds)

	// 2. Fetch review comments via gh CLI.
	comments := fetchReviewCommentsViaCLI(ctx, owner, repo, prNumber, logger)

	// 3. Check round limit — escalate to crew if exceeded.
	if round > maxReviewRounds {
		result.Escalated = true
		escalateToCrewOnRoundLimit(ctx, hqBeads, convoy, prNumber, round, comments, townRoot, logger, gtPath)
		return result
	}

	// 4. Create feedback bead and dispatch polecat for rounds within limit.
	feedbackID, err := createFeedbackBead(hqBeads, convoy, prNumber, round, owner, repo, comments)
	if err != nil {
		result.Error = fmt.Sprintf("create feedback bead: %v", err)
		logger("Feedback: convoy %s: %s", convoy.ID, result.Error)
		return result
	}
	result.FeedbackID = feedbackID
	logger("Feedback: convoy %s: created feedback bead %s (round %d)", convoy.ID, feedbackID, round)

	if err := dispatchFeedbackPolecat(ctx, townRoot, convoy, feedbackID, gtPath, logger); err != nil {
		result.Error = fmt.Sprintf("dispatch polecat: %v", err)
		logger("Feedback: convoy %s: %s", convoy.ID, result.Error)
		return result
	}

	return result
}

// escalateToCrewOnRoundLimit sends mail to the convoy owner (crew) with a full
// review summary and sets the convoy status to review_escalated.
func escalateToCrewOnRoundLimit(ctx context.Context, hqBeads *beads.Beads, convoy *beads.Issue, prNumber, round int, comments []reviewComment, townRoot string, logger func(format string, args ...interface{}), gtPath string) {
	logger("Feedback: convoy %s PR #%d: round %d exceeds limit (%d) — escalating to crew",
		convoy.ID, prNumber, round, maxReviewRounds)

	// Update convoy status to review_escalated.
	newDesc := setMetadataFields(convoy.Description, map[string]string{
		"convoy_status": "review_escalated",
		"review_round":  strconv.Itoa(round),
	})
	if err := hqBeads.Update(convoy.ID, beads.UpdateOptions{Description: &newDesc}); err != nil {
		logger("Feedback: convoy %s: failed to set review_escalated: %v", convoy.ID, err)
	}

	// Build review summary and mail body.
	commentSummary := formatReviewSummary(comments)
	mailBody := buildEscalationMailBody(convoy.ID, round, prNumber, commentSummary)
	subject := fmt.Sprintf("REVIEW_ESCALATION: %s PR #%d (round %d)", convoy.ID, prNumber, round)

	// Send mail to convoy owner (crew) — mail survives session death.
	convoyFields := beads.ParseConvoyFields(&beads.Issue{Description: convoy.Description})
	for _, addr := range convoyFields.NotificationAddresses() {
		sendMail(gtPath, townRoot, addr, subject, mailBody, logger, convoy.ID)
	}

	// Also nudge for immediate visibility.
	nudgeMsg := fmt.Sprintf("REVIEW_ESCALATION: convoy=%s pr=#%d round=%d — review limit exceeded, see mail",
		convoy.ID, prNumber, round)
	for _, addr := range convoyFields.NudgeNotificationAddresses() {
		nudgeCmd := exec.CommandContext(ctx, gtPath, "nudge", addr, nudgeMsg)
		nudgeCmd.Dir = townRoot
		util.SetProcessGroup(nudgeCmd)
		_ = nudgeCmd.Run()
	}
}

// sendMail sends a permanent mail message via gt mail send.
func sendMail(gtPath, townRoot, addr, subject, body string, logger func(format string, args ...interface{}), convoyID string) {
	mailCmd := exec.Command(gtPath, "mail", "send", addr,
		"-s", subject,
		"-m", body)
	mailCmd.Dir = townRoot
	util.SetProcessGroup(mailCmd)
	if err := mailCmd.Run(); err != nil {
		logger("Feedback: convoy %s: failed to mail %s: %v", convoyID, addr, err)
	}
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

// setMetadataFields updates multiple key: value metadata fields in a description.
// Adds the field at the top if not already present.
func setMetadataFields(description string, fields map[string]string) string {
	for k, v := range fields {
		lines := strings.Split(description, "\n")
		lowerKey := strings.ToLower(k) + ":"
		found := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(trimmed), lowerKey) {
				lines[i] = k + ": " + v
				found = true
				break
			}
		}
		if !found {
			lines = append([]string{k + ": " + v}, lines...)
		}
		description = strings.Join(lines, "\n")
	}
	return description
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

// ghReviewComment is the JSON shape returned by gh api for PR review comments.
type ghReviewComment struct {
	ID      int64  `json:"id"`
	User    struct{ Login string } `json:"user"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// fetchReviewCommentsViaCLI fetches PR review comments using the gh CLI.
func fetchReviewCommentsViaCLI(ctx context.Context, owner, repo string, prNumber int, logger func(format string, args ...interface{})) []reviewComment {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, prNumber)
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint, "--paginate")
	out, err := cmd.Output()
	if err != nil {
		logger("Feedback: failed to fetch review comments for %s/%s#%d: %v", owner, repo, prNumber, err)
		return nil
	}

	var raw []ghReviewComment
	if err := json.Unmarshal(out, &raw); err != nil {
		logger("Feedback: failed to parse review comments: %v", err)
		return nil
	}

	comments := make([]reviewComment, len(raw))
	for i, r := range raw {
		comments[i] = reviewComment{
			ID:      r.ID,
			User:    r.User.Login,
			Path:    r.Path,
			Line:    r.Line,
			Body:    r.Body,
			HTMLURL: r.HTMLURL,
		}
	}
	return comments
}

// createFeedbackBead creates a bead with review comments for a polecat to address.
func createFeedbackBead(hqBeads *beads.Beads, convoy *beads.Issue, prNumber, round int, owner, repo string, comments []reviewComment) (string, error) {
	integrationBranch := beads.GetIntegrationBranchField(convoy.Description)
	prURL := beads.GetPRURLField(convoy.Description)

	title := fmt.Sprintf("Address PR review feedback (round %d) for %s", round, convoy.ID)

	var desc strings.Builder
	fmt.Fprintf(&desc, "convoy_id: %s\n", convoy.ID)
	fmt.Fprintf(&desc, "pr_number: %d\n", prNumber)
	fmt.Fprintf(&desc, "pr_url: %s\n", prURL)
	fmt.Fprintf(&desc, "integration_branch: %s\n", integrationBranch)
	fmt.Fprintf(&desc, "review_round: %d\n", round)
	fmt.Fprintf(&desc, "github_owner: %s\n", owner)
	fmt.Fprintf(&desc, "github_repo: %s\n", repo)
	fmt.Fprintf(&desc, "merge_strategy: batch-pr\n")

	desc.WriteString("\n## Context\n")
	fmt.Fprintf(&desc, "The PR for convoy %s received CHANGES_REQUESTED (round %d).\n", convoy.ID, round)
	desc.WriteString("Address each review comment and push fixes to the integration branch.\n")

	desc.WriteString("\n## Review Comments\n")
	desc.WriteString(formatReviewSummary(comments))

	issue, err := hqBeads.Create(beads.CreateOptions{
		Title:       title,
		Type:        "task",
		Priority:    2,
		Description: desc.String(),
	})
	if err != nil {
		return "", err
	}
	return issue.ID, nil
}

// dispatchFeedbackPolecat slings a polecat to work on the feedback bead.
func dispatchFeedbackPolecat(ctx context.Context, townRoot string, convoy *beads.Issue, feedbackID, gtPath string, logger func(format string, args ...interface{})) error {
	slingArgs := []string{"sling", feedbackID}

	// Determine rig name from the gtPath's working directory.
	if baseBranch := beads.GetIntegrationBranchField(convoy.Description); baseBranch != "" {
		slingArgs = append(slingArgs, "--base-branch="+baseBranch)
	}

	slingCmd := exec.CommandContext(ctx, gtPath, slingArgs...)
	slingCmd.Dir = townRoot
	util.SetProcessGroup(slingCmd)
	if err := slingCmd.Run(); err != nil {
		return fmt.Errorf("gt sling %s: %w", feedbackID, err)
	}

	logger("Feedback: dispatched polecat for feedback bead %s", feedbackID)
	return nil
}
