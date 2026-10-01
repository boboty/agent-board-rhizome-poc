//go:build integration

package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// boardTaskCard returns the current workflow card for one issue as the CLI JSON
// surface reports it, so a lifecycle assertion reads the same projection a
// person sees rather than an internal field.
func boardTaskCard(t *testing.T, env integrationEnvironment, displayID string) boardWorkflowCard {
	t.Helper()
	body := runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--format", "json")
	workflow := decodeBoardWorkflow(t, body)
	assertFourTaskLevelColumns(t, workflow)
	for _, card := range workflow.Cards {
		if card.IssueDisplayID == displayID {
			return card
		}
	}
	t.Fatalf("issue %s is in no workflow column", displayID)
	return boardWorkflowCard{}
}

// assertFourTaskLevelColumns pins the whole board surface: exactly the four
// task-level columns, in order, and no card carrying a retired process-phase
// column.
func assertFourTaskLevelColumns(t *testing.T, workflow boardWorkflowPayload) {
	t.Helper()
	wantTitles := []string{"READY", "IN PROGRESS", "DONE", "BLOCKED"}
	if len(workflow.Columns) != len(wantTitles) {
		t.Fatalf("columns = %#v, want exactly %d", workflow.Columns, len(wantTitles))
	}
	for index, column := range workflow.Columns {
		if column.Title != wantTitles[index] {
			t.Fatalf("column %d = %q, want %q", index, column.Title, wantTitles[index])
		}
		switch column.Column {
		case "ready", "in_progress", "done", "blocked":
		default:
			t.Fatalf("column %d = %q, want a task-level column", index, column.Column)
		}
	}
	for _, card := range workflow.Cards {
		switch card.Column {
		case "ready", "in_progress", "done", "blocked":
		default:
			t.Fatalf("card %s is in retired column %q", card.IssueDisplayID, card.Column)
		}
	}
}

// assertBoardStage reads the board and fails with the stage name when an issue
// is not where the task-level model says it must be.
func assertBoardStage(t *testing.T, env integrationEnvironment, displayID, wantColumn, stage string) boardWorkflowCard {
	t.Helper()
	card := boardTaskCard(t, env, displayID)
	if card.Column != wantColumn {
		t.Fatalf("%s: %s column = %q, want %q (card %#v)", stage, displayID, card.Column, wantColumn, card)
	}
	return card
}

func boardWorkflowCreateReviewRequest(t *testing.T, session *mcp.ClientSession, issueID string, version, eventID int64) {
	t.Helper()
	result := callIntegrationTool(t, session, "create_review_request", map[string]any{
		"issue_id": issueID, "target_issue_version": version, "target_event_id": eventID,
	})
	var request struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	decodeIntegrationResult(t, result, &request)
	if result.IsError || request.ID == "" || request.Status != "open" {
		t.Fatalf("create_review_request for %s result = %#v, decoded = %#v", issueID, result, request)
	}
}

// TestIntegrationBoardTaskLevelLifecycleStaysInFourColumns is AB-5 acceptance
// #14 end to end against real SQLite and the real tool/HTTP surfaces: a task
// walks READY -> Orchestrator starts -> developer active -> verification ->
// changes-requested rework -> re-verification -> PASS -> DONE, and a second
// task walks IN PROGRESS -> BLOCKED. Every stage is asserted through the board
// projection, and no stage ever creates a fifth column.
//
// The Orchestrator is the sole owner of task-level state transitions.
// Developer and Verifier execute work and return evidence; they never directly
// advance the board-level state. Throughout the Developer -> Verification ->
// RC -> Correction -> Reverification -> PASS cycle, the task stays IN PROGRESS.
// Only PASS moves to DONE, and only an explicit stop (blocked review) moves
// to BLOCKED.
func TestIntegrationBoardTaskLevelLifecycleStaysInFourColumns(t *testing.T) {
	t.Parallel()
	env := newIntegrationEnvironment(t)
	session := env.connect(t)

	created := callIntegrationTool(t, session, "create_agent_session", map[string]any{
		"client_name": "Codex CLI", "agent_label": "Nova", "model": "gpt-5",
		"instance_key": "task-level-1", "worktree": "/tmp/wt/AB-5",
	})
	var sessionOutput struct {
		Handle string `json:"agent_session_handle"`
	}
	decodeIntegrationResult(t, created, &sessionOutput)
	if created.IsError || sessionOutput.Handle == "" {
		t.Fatalf("create_agent_session result = %#v, decoded = %#v", created, sessionOutput)
	}

	task := mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "AB-5 task-level lifecycle", "status": "ready", "priority": "high",
	})

	// READY: admitted, nobody working on it.
	assertBoardStage(t, env, task.DisplayID, "ready", "created")

	// Developer active -> IN PROGRESS, with the claiming session as detail.
	workClaim := boardWorkflowClaimIssue(t, session, task.DisplayID, sessionOutput.Handle)
	if workClaim.Attempt.Kind != "work" {
		t.Fatalf("developer claim kind = %q, want work", workClaim.Attempt.Kind)
	}
	inProgress := assertBoardStage(t, env, task.DisplayID, "in_progress", "developer active")
	if inProgress.AttemptKind != "work" {
		t.Fatalf("developer stage attempt kind = %q, want work", inProgress.AttemptKind)
	}
	if inProgress.ExecutorLabel == nil || *inProgress.ExecutorLabel != "Nova" {
		t.Fatalf("developer stage executor = %v, want the claiming session", inProgress.ExecutorLabel)
	}

	// Handed to review: still IN PROGRESS. Verification is a phase of the task,
	// not a column, and no attempt is active while it waits.
	completion := boardWorkflowFinish(t, session, workClaim, map[string]any{"target_issue_status": "review"})
	if completion.Issue.Status != "review" {
		t.Fatalf("issue status after review handoff = %q, want review", completion.Issue.Status)
	}
	awaiting := assertBoardStage(t, env, task.DisplayID, "in_progress", "awaiting verification")
	if awaiting.AttemptKind != "" {
		t.Fatalf("awaiting-verification card attempt kind = %q, want none", awaiting.AttemptKind)
	}
	boardWorkflowCreateReviewRequest(t, session, task.DisplayID, completion.Issue.Version, completion.LatestEventID)

	// An active review attempt is the same task-level state: verifying a task
	// is still executing it.
	reviewClaim := boardWorkflowClaimIssue(t, session, task.DisplayID, "")
	if reviewClaim.Attempt.Kind != "review" {
		t.Fatalf("review claim kind = %q, want review", reviewClaim.Attempt.Kind)
	}
	verifying := assertBoardStage(t, env, task.DisplayID, "in_progress", "verifier active")
	if verifying.AttemptKind != "review" {
		t.Fatalf("verifier stage attempt kind = %q, want review", verifying.AttemptKind)
	}

	// changes_requested: the task is stored ready again and has execution
	// history -> IN PROGRESS. Execution started, so it cannot fall back to
	// READY without the orchestrator explicitly moving it to BLOCKED. No RC
	// column appears.
	changes := boardWorkflowFinish(t, session, reviewClaim, map[string]any{"review_outcome": "changes_requested"})
	if changes.Issue.Status != "ready" {
		t.Fatalf("changes_requested left the issue in %q, want ready", changes.Issue.Status)
	}
	rework := assertBoardStage(t, env, task.DisplayID, "in_progress", "changes requested")
	if rework.ReviewStatus == nil || *rework.ReviewStatus != "changes_requested" || rework.ChangesRequestedCount != 1 {
		t.Fatalf("changes-requested card detail = %#v, want the failed round", rework)
	}
	if rework.AttemptKind != "" {
		t.Fatalf("rework card attempt kind = %q, want no active attempt (the round is history)", rework.AttemptKind)
	}

	// Reclaiming it works as a new work attempt — the ExecutionStarted flag
	// ensures it stayed IN PROGRESS the whole time, so no orchestrator could
	// have picked it up as fresh READY work while it was unattended.
	refixClaim := boardWorkflowClaimIssue(t, session, task.DisplayID, "")
	if refixClaim.Attempt.Kind != "work" {
		t.Fatalf("re-fix claim kind = %q, want work", refixClaim.Attempt.Kind)
	}
	assertBoardStage(t, env, task.DisplayID, "in_progress", "rework")

	// Re-verification, then a final PASS: approved -> done.
	refixCompletion := boardWorkflowFinish(t, session, refixClaim, map[string]any{"target_issue_status": "review"})
	assertBoardStage(t, env, task.DisplayID, "in_progress", "rework awaiting verification")
	boardWorkflowCreateReviewRequest(t, session, task.DisplayID, refixCompletion.Issue.Version, refixCompletion.LatestEventID)
	secondReview := boardWorkflowClaimIssue(t, session, task.DisplayID, "")
	assertBoardStage(t, env, task.DisplayID, "in_progress", "re-verification")
	approved := boardWorkflowFinish(t, session, secondReview, map[string]any{"review_outcome": "approved"})
	if approved.Issue.Status != "done" {
		t.Fatalf("approved review left the issue in %q, want done", approved.Issue.Status)
	}
	doneCard := assertBoardStage(t, env, task.DisplayID, "done", "final pass")
	if doneCard.ReviewStatus == nil || *doneCard.ReviewStatus != "approved" {
		t.Fatalf("DONE card review detail = %v, want the approving round", doneCard.ReviewStatus)
	}

	// IN PROGRESS -> cannot continue -> BLOCKED, with the reason as detail.
	blockedTask := mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "AB-5 blocked mid-flight", "status": "ready",
	})
	assertBoardStage(t, env, blockedTask.DisplayID, "ready", "blocked task created")
	blockedWorkClaim := boardWorkflowClaimIssue(t, session, blockedTask.DisplayID, "")
	assertBoardStage(t, env, blockedTask.DisplayID, "in_progress", "blocked task claimed")
	blockedCompletion := boardWorkflowFinish(t, session, blockedWorkClaim, map[string]any{"target_issue_status": "review"})
	assertBoardStage(t, env, blockedTask.DisplayID, "in_progress", "blocked task in review")
	boardWorkflowCreateReviewRequest(t, session, blockedTask.DisplayID, blockedCompletion.Issue.Version, blockedCompletion.LatestEventID)
	blockedReview := boardWorkflowClaimIssue(t, session, blockedTask.DisplayID, "")
	blockedFinish := boardWorkflowFinish(t, session, blockedReview, map[string]any{
		"review_outcome": "blocked", "blocked_reason": "vendor API unavailable",
	})
	if blockedFinish.Issue.Status != "blocked" {
		t.Fatalf("blocked review left the issue in %q, want blocked", blockedFinish.Issue.Status)
	}
	blockedCard := assertBoardStage(t, env, blockedTask.DisplayID, "blocked", "cannot continue")
	if blockedCard.BlockedReason == nil || *blockedCard.BlockedReason != "vendor API unavailable" {
		t.Fatalf("BLOCKED card reason = %v, want the stored reason", blockedCard.BlockedReason)
	}
	if blockedCard.ReviewStatus == nil || *blockedCard.ReviewStatus != "blocked" {
		t.Fatalf("BLOCKED card review detail = %v, want the blocking round", blockedCard.ReviewStatus)
	}

	// The HTTP surface projects the same thing: four columns, the finished task
	// in DONE and the stopped task in BLOCKED.
	server := launchIntegrationBoardServer(t, env, "127.0.0.1:0")
	t.Cleanup(func() { stopIntegrationBoardServer(t, server) })
	endpoint := server.waitForEndpoint(t)
	response, err := (&http.Client{Timeout: integrationTimeout}).Get(strings.TrimSuffix(endpoint, "/") + "/api/board")
	if err != nil {
		t.Fatalf("GET /api/board: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read /api/board: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/api/board status = %d, want 200", response.StatusCode)
	}
	var payload struct {
		Workflow boardWorkflowPayload `json:"workflow"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode /api/board: %v", err)
	}
	assertFourTaskLevelColumns(t, payload.Workflow)
	servedColumns := map[string]string{}
	for _, card := range payload.Workflow.Cards {
		servedColumns[card.IssueDisplayID] = card.Column
	}
	if servedColumns[task.DisplayID] != "done" || servedColumns[blockedTask.DisplayID] != "blocked" {
		t.Fatalf("served columns = %#v, want the finished task DONE and the stopped task BLOCKED", servedColumns)
	}
}
