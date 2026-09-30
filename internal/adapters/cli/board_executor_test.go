package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rhizome-mcp/internal/domain"
)

// boardExecutorFixture returns one board with a fully attributed active
// attempt and one claimed without an agent session, so every surface can be
// checked for both cases at once.
func boardExecutorFixture() domain.BoardResult {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	label, instance, client, model, worktree := "Luna", "worker-1", "Codex CLI", "gpt-5", "/tmp/wt/AB-2"
	return domain.BoardResult{
		GeneratedAt: now,
		StatusCounts: []domain.EffectiveStatusCount{
			{EffectiveStatus: domain.EffectiveStatusInProgress, Count: 2},
		},
		ActiveAttempts: []domain.ActiveAttemptSummary{
			{
				AttemptID: "attributed", IssueID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", IssueDisplayID: "ISSUE-1",
				IssueTitle: "Attributed work", Kind: domain.AttemptKindWork,
				SessionID: stringPtr("01ARZ3NDEKTSV4RRFFQ69G5FAW"), SessionLabel: &label,
				SessionInstanceKey: &instance, SessionClientName: &client, SessionModel: &model, SessionWorktree: &worktree,
				StartedAt: now.Add(-time.Hour), LeaseExpiresAt: now.Add(15 * time.Minute),
			},
			{
				AttemptID: "sessionless", IssueID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", IssueDisplayID: "ISSUE-2",
				IssueTitle: "Session-less work", Kind: domain.AttemptKindWork,
				StartedAt: now.Add(-time.Minute), LeaseExpiresAt: now.Add(30 * time.Minute),
			},
		},
		ActiveReservations: []domain.Reservation{}, BlockedIssues: []domain.IssueProjection{},
		ReviewRequests: []domain.ReviewRequest{}, AttemptGates: []domain.AttemptGateProgress{},
		PlanningGraph: domain.GraphResult{
			Nodes: []domain.IssueProjection{
				{Issue: domain.Issue{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DisplayID: "ISSUE-1"}, EffectiveStatus: domain.EffectiveStatusInProgress},
				{Issue: domain.Issue{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", DisplayID: "ISSUE-2"}, EffectiveStatus: domain.EffectiveStatusInProgress},
			},
			Edges: []domain.GraphEdge{}, EntryPoints: []string{}, BlockingNodes: []string{},
			Summary: domain.GraphSummary{NodeCount: 2, EdgeCount: 0, EntryPointCount: 0, BlockingNodeCount: 0},
		},
	}
}

// TestBoardExecutorMetadataIsConsistentAcrossJSONTableAndHTML pins acceptance
// AB-2 #1, #2, and #4: the same active attempt shows the same runtime
// information (agent label, instance key, harness/client name, model,
// worktree, lease expiry) in the JSON projection, the CLI table, and the
// static and served HTML pages, while an attempt claimed without a session
// degrades to omitted JSON fields, empty table cells, and the HTML
// placeholder instead of failing or disappearing.
func TestBoardExecutorMetadataIsConsistentAcrossJSONTableAndHTML(t *testing.T) {
	result := boardExecutorFixture()

	response := boardResponseFromDomain(result)
	if len(response.ActiveAttempts) != 2 {
		t.Fatalf("JSON attempts = %d, want 2", len(response.ActiveAttempts))
	}
	attributed := response.ActiveAttempts[0]
	if attributed.SessionLabel == nil || *attributed.SessionLabel != "Luna" ||
		attributed.SessionInstanceKey == nil || *attributed.SessionInstanceKey != "worker-1" ||
		attributed.SessionClientName == nil || *attributed.SessionClientName != "Codex CLI" ||
		attributed.SessionModel == nil || *attributed.SessionModel != "gpt-5" ||
		attributed.SessionWorktree == nil || *attributed.SessionWorktree != "/tmp/wt/AB-2" {
		t.Fatalf("JSON projection = %#v", attributed)
	}
	if !attributed.LeaseExpiresAt.Equal(result.ActiveAttempts[0].LeaseExpiresAt) {
		t.Fatalf("JSON lease expiry = %v", attributed.LeaseExpiresAt)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session_label", "session_instance_key", "session_client_name", "session_model", "session_worktree", "lease_expires_at"} {
		if !strings.Contains(string(encoded), `"`+key+`"`) {
			t.Fatalf("JSON payload is missing %q: %s", key, encoded)
		}
	}
	// The session-less attempt omits every session field rather than emitting
	// a misleading empty value.
	sessionlessJSON, err := json.Marshal(response.ActiveAttempts[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session_id", "session_label", "session_instance_key", "session_client_name", "session_model", "session_worktree"} {
		if strings.Contains(string(sessionlessJSON), `"`+key+`"`) {
			t.Fatalf("session-less attempt emitted %q: %s", key, sessionlessJSON)
		}
	}

	var stdout bytes.Buffer
	cli := New(Services{}, &stdout, nil, nil, nil)
	if err := cli.writeBoardTable(result); err != nil {
		t.Fatalf("writeBoardTable: %v", err)
	}
	table := stdout.String()
	header := "attempt_id\tissue\tkind\tsession_label\tsession_instance_key\tsession_client_name\tsession_model\tsession_worktree\tlease_expires_at"
	if !strings.Contains(table, header) {
		t.Fatalf("table header = %q, want %q", table, header)
	}
	wantRow := "attributed\tISSUE-1\twork\tLuna\tworker-1\tCodex CLI\tgpt-5\t/tmp/wt/AB-2\t" + result.ActiveAttempts[0].LeaseExpiresAt.Format(time.RFC3339Nano)
	if !strings.Contains(table, wantRow) {
		t.Fatalf("table rows are missing the attributed attempt:\n%s", table)
	}
	wantEmptyRow := "sessionless\tISSUE-2\twork\t\t\t\t\t\t" + result.ActiveAttempts[1].LeaseExpiresAt.Format(time.RFC3339Nano)
	if !strings.Contains(table, wantEmptyRow) {
		t.Fatalf("table rows are missing the session-less attempt:\n%s", table)
	}

	staticHTML, err := renderBoardHTML(result)
	if err != nil {
		t.Fatalf("renderBoardHTML: %v", err)
	}
	servedHTML, err := renderServedBoardHTML(result)
	if err != nil {
		t.Fatalf("renderServedBoardHTML: %v", err)
	}
	for name, html := range map[string]string{"static": staticHTML, "served": servedHTML} {
		for _, want := range []string{"<th>Instance key</th>", "<th>Client</th>", "<th>Model</th>", "<th>Worktree</th>", "worker-1", "Codex CLI", "gpt-5", "/tmp/wt/AB-2"} {
			if !strings.Contains(html, want) {
				t.Fatalf("%s HTML is missing %q", name, want)
			}
		}
		sessionlessRow := tableRowFor(t, html, "sessionless")
		// Five session fields plus reservations and gates degrade to the em
		// dash placeholder for a session-less attempt.
		if got := strings.Count(sessionlessRow, "—"); got != 7 {
			t.Fatalf("%s session-less row placeholders = %d, want 7: %s", name, got, sessionlessRow)
		}
		attributedRow := tableRowFor(t, html, "attributed")
		// Only the reservation and gate columns are empty for this attempt;
		// every session metadata cell carries the reported value.
		if got := strings.Count(attributedRow, "—"); got != 2 {
			t.Fatalf("%s attributed row placeholders = %d, want 2 (reservations and gates): %s", name, got, attributedRow)
		}
	}
}

// TestBoardHTTPExecutorMetadataDrivesJSONAndETag proves the loopback JSON API
// carries the same runtime information and that its semantic ETag changes when
// only that information changes -- otherwise a live-refreshing board would
// keep serving a cached body after the executor metadata moved.
func TestBoardHTTPExecutorMetadataDrivesJSONAndETag(t *testing.T) {
	board := boardExecutorFixture()
	handler := NewBoardHTTPHandler(&stubBoardService{board: board})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/board", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var payload BoardResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.ActiveAttempts) != 2 {
		t.Fatalf("JSON attempts = %d, want 2", len(payload.ActiveAttempts))
	}
	attributed := payload.ActiveAttempts[0]
	if attributed.SessionInstanceKey == nil || *attributed.SessionInstanceKey != "worker-1" ||
		attributed.SessionClientName == nil || *attributed.SessionClientName != "Codex CLI" ||
		attributed.SessionModel == nil || *attributed.SessionModel != "gpt-5" ||
		attributed.SessionWorktree == nil || *attributed.SessionWorktree != "/tmp/wt/AB-2" {
		t.Fatalf("HTTP JSON projection = %#v", attributed)
	}
	if payload.ActiveAttempts[1].SessionWorktree != nil {
		t.Fatalf("session-less attempt exposed a worktree: %#v", payload.ActiveAttempts[1])
	}

	baseline := semanticBoardETag(board)
	if baseline == "" || baseline != semanticBoardETag(boardExecutorFixture()) {
		t.Fatalf("ETag is not stable for identical boards: %q", baseline)
	}
	// Every executor field the JSON body carries must move the ETag;
	// otherwise a polling board would keep a cached body after only that
	// field changed.
	fieldMutations := []struct {
		field  string
		mutate func(attempt *domain.ActiveAttemptSummary)
	}{
		{"session_id", func(attempt *domain.ActiveAttemptSummary) {
			attempt.SessionID = stringPtr("01ARZ3NDEKTSV4RRFFQ69G5FAZ")
		}},
		{"session_label", func(attempt *domain.ActiveAttemptSummary) { attempt.SessionLabel = stringPtr("Other agent") }},
		{"session_instance_key", func(attempt *domain.ActiveAttemptSummary) { attempt.SessionInstanceKey = stringPtr("worker-2") }},
		{"session_client_name", func(attempt *domain.ActiveAttemptSummary) { attempt.SessionClientName = stringPtr("Claude Code") }},
		{"session_model", func(attempt *domain.ActiveAttemptSummary) { attempt.SessionModel = stringPtr("sonnet") }},
		{"session_worktree", func(attempt *domain.ActiveAttemptSummary) {
			attempt.SessionWorktree = stringPtr("/tmp/wt/AB-2-renamed")
		}},
	}
	for _, testCase := range fieldMutations {
		moved := boardExecutorFixture()
		testCase.mutate(&moved.ActiveAttempts[0])
		if semanticBoardETag(moved) == baseline {
			t.Fatalf("ETag ignored a %s change, so the served board would not refresh executor metadata", testCase.field)
		}
	}
	// Clearing a field must also move the ETag: an attempt whose session
	// metadata disappeared is a different board.
	cleared := boardExecutorFixture()
	cleared.ActiveAttempts[0].SessionWorktree = nil
	if semanticBoardETag(cleared) == baseline {
		t.Fatal("ETag ignored a cleared worktree")
	}
}
