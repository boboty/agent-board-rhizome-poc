//go:build integration

package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// boardWorkflowIssueRefs names the issues one seeded lifecycle produced, so a
// test can assert a column per real state rather than per synthetic fixture.
type boardWorkflowIssueRefs struct {
	ready         boardIssueRef
	notReady      boardIssueRef
	inProgress    boardIssueRef
	verifyingOpen boardIssueRef
	verifyingBusy boardIssueRef
	changesAsked  boardIssueRef
	decision      boardIssueRef
	done          boardIssueRef
	sessionHandle string
}

type boardWorkflowClaim struct {
	Attempt struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"attempt"`
	LeaseToken string `json:"lease_token"`
}

type boardWorkflowCompletion struct {
	Issue struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	} `json:"issue"`
	LatestEventID int64 `json:"latest_event_id"`
}

func boardWorkflowClaimIssue(t *testing.T, session *mcp.ClientSession, issueID, handle string) boardWorkflowClaim {
	t.Helper()
	arguments := map[string]any{"issue_id": issueID, "lease_seconds": 600}
	if handle != "" {
		arguments["agent_session_handle"] = handle
	}
	result := callIntegrationTool(t, session, "claim_issue", arguments)
	var claim boardWorkflowClaim
	decodeIntegrationResult(t, result, &claim)
	if result.IsError || claim.Attempt.ID == "" || claim.LeaseToken == "" {
		t.Fatalf("claim_issue %s result = %#v, decoded = %#v", issueID, result, claim)
	}
	return claim
}

func boardWorkflowFinish(t *testing.T, session *mcp.ClientSession, claim boardWorkflowClaim, extra map[string]any) boardWorkflowCompletion {
	t.Helper()
	arguments := map[string]any{
		"attempt_id": claim.Attempt.ID, "lease_token": claim.LeaseToken,
		"outcome": "completed", "result_summary": "workflow projection fixture",
	}
	for key, value := range extra {
		arguments[key] = value
	}
	result := callIntegrationTool(t, session, "finish_attempt", arguments)
	var completion boardWorkflowCompletion
	decodeIntegrationResult(t, result, &completion)
	if result.IsError {
		t.Fatalf("finish_attempt result = %#v, decoded = %#v", result, completion)
	}
	return completion
}

// boardWorkflowHandToReview drives a ready issue through the real gated
// complete_work_to_review path and returns the frozen target the review
// request must pin.
func boardWorkflowHandToReview(t *testing.T, session *mcp.ClientSession, issueID string) (int64, int64) {
	t.Helper()
	work := boardWorkflowClaimIssue(t, session, issueID, "")
	completion := boardWorkflowFinish(t, session, work, map[string]any{"target_issue_status": "review"})
	if completion.Issue.Status != "review" || completion.Issue.Version < 1 {
		t.Fatalf("issue %s after review handoff = %#v, want stored review", issueID, completion.Issue)
	}
	return completion.Issue.Version, completion.LatestEventID
}

func boardWorkflowOpenReview(t *testing.T, session *mcp.ClientSession, issueID string) {
	t.Helper()
	version, eventID := boardWorkflowHandToReview(t, session, issueID)
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

// seedBoardWorkflowLifecycle builds one real issue per workflow column through
// the advertised MCP tools only: a READY task with a queue rank, an unstarted
// open task, an active work attempt claimed by a session with full runtime
// metadata, an open review, a claimed review attempt, a changes-requested
// round, a review blocked pending a decision, and a done task carrying a
// commit artifact.
func seedBoardWorkflowLifecycle(t *testing.T, env integrationEnvironment, session *mcp.ClientSession) boardWorkflowIssueRefs {
	t.Helper()
	refs := boardWorkflowIssueRefs{}

	created := callIntegrationTool(t, session, "create_agent_session", map[string]any{
		"client_name": "Codex CLI", "agent_label": "Luna", "model": "gpt-5",
		"instance_key": "worker-1", "worktree": "/tmp/wt/AB-3",
	})
	var sessionOutput struct {
		Handle string `json:"agent_session_handle"`
	}
	decodeIntegrationResult(t, created, &sessionOutput)
	if created.IsError || sessionOutput.Handle == "" {
		t.Fatalf("create_agent_session result = %#v, decoded = %#v", created, sessionOutput)
	}
	refs.sessionHandle = sessionOutput.Handle

	refs.ready = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Ready work", "status": "ready", "priority": "high", "ready_rank": 7,
	})
	refs.notReady = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Not admitted yet", "status": "open",
	})

	refs.inProgress = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Active work", "status": "ready", "priority": "critical",
	})
	if claim := boardWorkflowClaimIssue(t, session, refs.inProgress.DisplayID, refs.sessionHandle); claim.Attempt.Kind != "work" {
		t.Fatalf("in-progress claim kind = %q, want work", claim.Attempt.Kind)
	}

	refs.verifyingOpen = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Awaiting review", "status": "ready",
	})
	boardWorkflowOpenReview(t, session, refs.verifyingOpen.DisplayID)

	refs.verifyingBusy = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Under review", "status": "ready",
	})
	boardWorkflowOpenReview(t, session, refs.verifyingBusy.DisplayID)
	if claim := boardWorkflowClaimIssue(t, session, refs.verifyingBusy.DisplayID, ""); claim.Attempt.Kind != "review" {
		t.Fatalf("verifying claim kind = %q, want review", claim.Attempt.Kind)
	}

	refs.changesAsked = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Sent back for rework", "status": "ready",
	})
	boardWorkflowOpenReview(t, session, refs.changesAsked.DisplayID)
	changesClaim := boardWorkflowClaimIssue(t, session, refs.changesAsked.DisplayID, "")
	changes := boardWorkflowFinish(t, session, changesClaim, map[string]any{"review_outcome": "changes_requested"})
	if changes.Issue.Status != "ready" {
		t.Fatalf("changes_requested left issue %s in %q, want ready", refs.changesAsked.DisplayID, changes.Issue.Status)
	}

	refs.decision = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Needs a human decision", "status": "ready",
	})
	boardWorkflowOpenReview(t, session, refs.decision.DisplayID)
	decisionClaim := boardWorkflowClaimIssue(t, session, refs.decision.DisplayID, "")
	blocked := boardWorkflowFinish(t, session, decisionClaim, map[string]any{
		"review_outcome": "blocked", "blocked_reason": "product decision required",
	})
	if blocked.Issue.Status != "blocked" {
		t.Fatalf("blocked review left issue %s in %q, want blocked", refs.decision.DisplayID, blocked.Issue.Status)
	}

	refs.done = mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Delivered work", "status": "ready",
	})
	doneWork := boardWorkflowClaimIssue(t, session, refs.done.DisplayID, "")
	title := "feat: workflow projection"
	completion := boardWorkflowFinish(t, session, doneWork, map[string]any{
		"target_issue_status": "done",
		"artifacts": []map[string]any{
			{"type": "commit", "uri": "abc1234", "title": title},
			{"type": "file", "uri": "docs/notes.md"},
		},
	})
	if completion.Issue.Status != "done" {
		t.Fatalf("done completion left issue %s in %q", refs.done.DisplayID, completion.Issue.Status)
	}
	return refs
}

// boardWorkflowCard is the JSON projection of one card, transcribed so the
// integration test asserts the documented contract instead of the Go type.
type boardWorkflowCard struct {
	Column                string  `json:"column"`
	IssueID               string  `json:"issue_id"`
	IssueDisplayID        string  `json:"issue_display_id"`
	Title                 string  `json:"title"`
	Priority              string  `json:"priority"`
	ReadyRank             *int64  `json:"ready_rank"`
	IsClaimable           bool    `json:"is_claimable"`
	AttemptKind           string  `json:"attempt_kind"`
	ExecutorLabel         *string `json:"executor_label"`
	ExecutorInstanceKey   *string `json:"executor_instance_key"`
	ExecutorClient        *string `json:"executor_client"`
	ExecutorModel         *string `json:"executor_model"`
	ExecutorWorktree      *string `json:"executor_worktree"`
	ReviewStatus          *string `json:"review_status"`
	ChangesRequestedCount int     `json:"changes_requested_count"`
	Delivery              []struct {
		Type  string  `json:"type"`
		URI   string  `json:"uri"`
		Title *string `json:"title"`
	} `json:"delivery"`
}

type boardWorkflowPayload struct {
	Columns []struct {
		Column string `json:"column"`
		Title  string `json:"title"`
		Count  int    `json:"count"`
	} `json:"columns"`
	Cards       []boardWorkflowCard `json:"cards"`
	Unprojected []struct {
		IssueID        string `json:"issue_id"`
		IssueDisplayID string `json:"issue_display_id"`
		Reason         string `json:"reason"`
	} `json:"unprojected"`
	Truncation map[string]bool `json:"truncation"`
}

func decodeBoardWorkflow(t *testing.T, body []byte) boardWorkflowPayload {
	t.Helper()
	var payload struct {
		Workflow boardWorkflowPayload `json:"workflow"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode board workflow: %v\nbody:\n%s", err, body)
	}
	return payload.Workflow
}

// TestIntegrationBoardWorkflowProjectsRealLifecycle is acceptance AB-3 #1-#4
// end to end through the real binary: after seeding every workflow state, each
// issue appears in exactly one correct column, the IN PROGRESS card carries the
// claiming session's runtime metadata, the DONE card carries its commit, and
// the RC card reports the changes-requested round.
func TestIntegrationBoardWorkflowProjectsRealLifecycle(t *testing.T) {
	t.Parallel()
	env := newIntegrationEnvironment(t)
	session := env.connect(t)
	refs := seedBoardWorkflowLifecycle(t, env, session)

	body := runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--format", "json")
	workflow := decodeBoardWorkflow(t, body)

	wantColumns := map[string]string{
		refs.ready.DisplayID:         "ready",
		refs.inProgress.DisplayID:    "in_progress",
		refs.verifyingOpen.DisplayID: "verifying",
		refs.verifyingBusy.DisplayID: "verifying",
		refs.changesAsked.DisplayID:  "rc",
		refs.decision.DisplayID:      "decision_required",
		refs.done.DisplayID:          "done",
	}
	columns := map[string]string{}
	cards := map[string]boardWorkflowCard{}
	for _, card := range workflow.Cards {
		columns[card.IssueDisplayID] = card.Column
		cards[card.IssueDisplayID] = card
	}
	for displayID, wantColumn := range wantColumns {
		if columns[displayID] != wantColumn {
			t.Fatalf("issue %s column = %q, want %q (cards: %#v)", displayID, columns[displayID], wantColumn, columns)
		}
	}

	// Acceptance #2: one placement per issue, cards and unprojected together.
	placements := map[string]int{}
	for _, card := range workflow.Cards {
		placements[card.IssueID]++
	}
	for _, item := range workflow.Unprojected {
		placements[item.IssueID]++
	}
	allIssues := []boardIssueRef{
		refs.ready, refs.notReady, refs.inProgress, refs.verifyingOpen,
		refs.verifyingBusy, refs.changesAsked, refs.decision, refs.done,
	}
	for _, issue := range allIssues {
		if placements[issue.ID] != 1 {
			t.Fatalf("issue %s placement count = %d, want exactly 1", issue.DisplayID, placements[issue.ID])
		}
	}
	if columns[refs.notReady.DisplayID] != "" {
		t.Fatalf("open issue %s was placed in column %q", refs.notReady.DisplayID, columns[refs.notReady.DisplayID])
	}
	unprojectedReason := ""
	for _, item := range workflow.Unprojected {
		if item.IssueDisplayID == refs.notReady.DisplayID {
			unprojectedReason = item.Reason
		}
	}
	if unprojectedReason != "not_ready" {
		t.Fatalf("open issue unprojected reason = %q, want not_ready", unprojectedReason)
	}

	// Every column heading is present, in order, and its count matches its cards.
	wantTitles := []string{"READY", "IN PROGRESS", "VERIFYING", "RC", "DECISION REQUIRED", "DONE"}
	if len(workflow.Columns) != len(wantTitles) {
		t.Fatalf("columns = %#v, want %d", workflow.Columns, len(wantTitles))
	}
	for index, column := range workflow.Columns {
		if column.Title != wantTitles[index] {
			t.Fatalf("column %d title = %q, want %q", index, column.Title, wantTitles[index])
		}
		counted := 0
		for _, card := range workflow.Cards {
			if card.Column == column.Column {
				counted++
			}
		}
		if column.Count != counted {
			t.Fatalf("column %q count = %d, want %d", column.Column, column.Count, counted)
		}
	}

	// Acceptance #3: the IN PROGRESS card shows the real session runtime.
	inProgressCard := cards[refs.inProgress.DisplayID]
	if inProgressCard.AttemptKind != "work" ||
		inProgressCard.ExecutorLabel == nil || *inProgressCard.ExecutorLabel != "Luna" ||
		inProgressCard.ExecutorInstanceKey == nil || *inProgressCard.ExecutorInstanceKey != "worker-1" ||
		inProgressCard.ExecutorClient == nil || *inProgressCard.ExecutorClient != "Codex CLI" ||
		inProgressCard.ExecutorModel == nil || *inProgressCard.ExecutorModel != "gpt-5" ||
		inProgressCard.ExecutorWorktree == nil || *inProgressCard.ExecutorWorktree != "/tmp/wt/AB-3" {
		t.Fatalf("IN PROGRESS card runtime metadata = %#v", inProgressCard)
	}

	// READY rank survives into the card; the DONE card carries its commit.
	if cards[refs.ready.DisplayID].ReadyRank == nil || *cards[refs.ready.DisplayID].ReadyRank != 7 {
		t.Fatalf("READY card rank = %v, want 7", cards[refs.ready.DisplayID].ReadyRank)
	}
	doneCard := cards[refs.done.DisplayID]
	if len(doneCard.Delivery) != 1 || doneCard.Delivery[0].Type != "commit" ||
		doneCard.Delivery[0].URI != "abc1234" || doneCard.Delivery[0].Title == nil ||
		*doneCard.Delivery[0].Title != "feat: workflow projection" {
		t.Fatalf("DONE card delivery = %#v, want the commit artifact only", doneCard.Delivery)
	}

	// The RC card reports the review round it came back from.
	rcCard := cards[refs.changesAsked.DisplayID]
	if rcCard.ReviewStatus == nil || *rcCard.ReviewStatus != "changes_requested" || rcCard.ChangesRequestedCount != 1 {
		t.Fatalf("RC card review signal = %#v, want one changes_requested round", rcCard)
	}

	// Nothing was cut on a fixture this small, so no flag may claim otherwise.
	for flag, set := range workflow.Truncation {
		if set {
			t.Fatalf("workflow.truncation.%s = true on a small fixture", flag)
		}
	}

	// The CLI table exposes the same projection.
	table := string(runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--format", "table"))
	for _, want := range []string{"workflow\ncolumn\ttitle\tcount", "workflow_cards", "workflow_unprojected",
		"in_progress\t" + refs.inProgress.DisplayID, "rc\t" + refs.changesAsked.DisplayID} {
		if !strings.Contains(table, want) {
			t.Fatalf("board table is missing %q:\n%s", want, table)
		}
	}
}

// TestIntegrationBoardWorkflowStaticAndServedHTML is acceptance AB-3 #5: both
// the archived static snapshot and the served web board render the Kanban
// read-only, with the same executor runtime metadata the JSON carries.
func TestIntegrationBoardWorkflowStaticAndServedHTML(t *testing.T) {
	t.Parallel()
	env := newIntegrationEnvironment(t)
	session := env.connect(t)
	refs := seedBoardWorkflowLifecycle(t, env, session)

	htmlPath := filepath.Join(t.TempDir(), "workflow.html")
	runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--output", htmlPath)
	staticBytes, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read static board snapshot: %v", err)
	}
	staticHTML := string(staticBytes)
	assertWorkflowHTML(t, "static snapshot", staticHTML, refs, false)

	server := launchIntegrationBoardServer(t, env, "127.0.0.1:0")
	t.Cleanup(func() { stopIntegrationBoardServer(t, server) })
	endpoint := server.waitForEndpoint(t)
	client := &http.Client{Timeout: integrationTimeout}

	pageResponse, err := client.Get(endpoint)
	if err != nil {
		t.Fatalf("get served board page: %v", err)
	}
	pageBytes, err := io.ReadAll(pageResponse.Body)
	pageResponse.Body.Close()
	if err != nil {
		t.Fatalf("read served board page: %v", err)
	}
	if pageResponse.StatusCode != http.StatusOK {
		t.Fatalf("served board page status = %d, want %d", pageResponse.StatusCode, http.StatusOK)
	}
	assertWorkflowHTML(t, "served board", string(pageBytes), refs, true)

	apiResponse, err := client.Get(strings.TrimSuffix(endpoint, "/") + "/api/board")
	if err != nil {
		t.Fatalf("get served board api: %v", err)
	}
	apiBytes, err := io.ReadAll(apiResponse.Body)
	apiResponse.Body.Close()
	if err != nil {
		t.Fatalf("read served board api: %v", err)
	}
	workflow := decodeBoardWorkflow(t, apiBytes)
	servedColumn := ""
	for _, card := range workflow.Cards {
		if card.IssueDisplayID == refs.inProgress.DisplayID {
			servedColumn = card.Column
		}
	}
	if servedColumn != "in_progress" {
		t.Fatalf("served /api/board column for %s = %q, want in_progress", refs.inProgress.DisplayID, servedColumn)
	}
}

func assertWorkflowHTML(t *testing.T, surface, html string, refs boardWorkflowIssueRefs, writable bool) {
	t.Helper()
	for _, want := range []string{
		"Workflow board", "kanban-column", "READY", "IN PROGRESS", "VERIFYING", "RC",
		"DECISION REQUIRED", "DONE",
		refs.inProgress.DisplayID, "Luna", "worker-1", "Codex CLI", "gpt-5", "/tmp/wt/AB-3",
		refs.changesAsked.DisplayID, "changes_requested",
		refs.done.DisplayID, "abc1234",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("%s is missing %q", surface, want)
		}
	}
	// The offline snapshot must stay a read-only artifact: it serves no routes,
	// so a write form there could only fail. The served board may carry the
	// minimal write forms, but never drag/drop affordances.
	lowered := strings.ToLower(html)
	for _, forbidden := range []string{"draggable=", "ondrop=", "ondragstart="} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("%s contains drag affordance %q", surface, forbidden)
		}
	}
	if writable {
		if !strings.Contains(lowered, "method=\"post\"") || !strings.Contains(lowered, "csrf_token") {
			t.Fatalf("%s is missing the write forms", surface)
		}
		return
	}
	for _, forbidden := range []string{"method=\"post\"", "method='post'", "csrf_token"} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("%s contains write affordance %q", surface, forbidden)
		}
	}
}
