package cli

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"rhizome-mcp/internal/domain"
)

// boardWorkflowFixture returns one otherwise-rich board whose new workflow
// projection carries exactly one card in each of the six columns, one
// deliberately unprojected issue, and every pre-existing board collection
// populated. The HTML, JSON, table, and ETag surfaces all read this fixture so
// the additive workflow field is checked against the same state everywhere.
func boardWorkflowFixture() domain.BoardResult {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	rank := int64(3)
	targetVersion := int64(4)
	startedAt := now.Add(-time.Hour)
	leaseExpiresAt := now.Add(15 * time.Minute)
	requestedAt := now.Add(-30 * time.Minute)
	label, instance, client, model, worktree := "Luna", "worker-1", "Codex CLI", "gpt-5", "/tmp/wt/ISSUE-102"
	verifierLabel := "Mira"
	reviewOpenID, reviewChangesID, reviewBlockedID := "review-open", "review-changes", "review-blocked"
	reviewOpen := domain.ReviewRequestStatusOpen
	reviewChanges := domain.ReviewRequestStatusChangesRequested
	reviewBlocked := domain.ReviewRequestStatusBlocked
	commitTitle := "fix: in progress work"

	cards := []domain.BoardWorkflowCard{
		{
			Column: domain.BoardWorkflowColumnReady, IssueID: "issue-101", IssueDisplayID: "ISSUE-101",
			Title: "Ready card", Type: domain.TypeTask, Priority: domain.PriorityMedium,
			StoredStatus: domain.StatusReady, ReadyRank: &rank, IsClaimable: true,
		},
		{
			Column: domain.BoardWorkflowColumnInProgress, IssueID: "issue-102", IssueDisplayID: "ISSUE-102",
			Title: "In progress card", Type: domain.TypeTask, Priority: domain.PriorityHigh,
			StoredStatus: domain.StatusReady,
			AttemptID:    "attempt-work", AttemptKind: domain.AttemptKindWork,
			ExecutorLabel: &label, ExecutorInstanceKey: &instance, ExecutorClient: &client,
			ExecutorModel: &model, ExecutorWorktree: &worktree,
			AttemptStartedAt: &startedAt, LeaseExpiresAt: &leaseExpiresAt,
			Delivery: []domain.BoardDeliveryReference{{Type: domain.ArtifactTypeCommit, URI: "abc1234", Title: &commitTitle}},
		},
		{
			Column: domain.BoardWorkflowColumnVerifying, IssueID: "issue-103", IssueDisplayID: "ISSUE-103",
			Title: "Verifying card", Type: domain.TypeTask, Priority: domain.PriorityMedium,
			StoredStatus: domain.StatusReview,
			AttemptID:    "attempt-review", AttemptKind: domain.AttemptKindReview,
			ExecutorLabel:   &verifierLabel,
			ReviewRequestID: &reviewOpenID, ReviewStatus: &reviewOpen,
			ReviewTargetVersion: &targetVersion, ReviewRequestedAt: &requestedAt,
		},
		{
			Column: domain.BoardWorkflowColumnRC, IssueID: "issue-104", IssueDisplayID: "ISSUE-104",
			Title: "Changes requested card", Type: domain.TypeBug, Priority: domain.PriorityCritical,
			StoredStatus:    domain.StatusReady,
			ReviewRequestID: &reviewChangesID, ReviewStatus: &reviewChanges, ChangesRequestedCount: 2,
		},
		{
			Column: domain.BoardWorkflowColumnDecisionRequired, IssueID: "issue-105", IssueDisplayID: "ISSUE-105",
			Title: "Decision required card", Type: domain.TypeTask, Priority: domain.PriorityLow,
			StoredStatus:    domain.StatusBlocked,
			ReviewRequestID: &reviewBlockedID, ReviewStatus: &reviewBlocked,
		},
		{
			Column: domain.BoardWorkflowColumnDone, IssueID: "issue-106", IssueDisplayID: "ISSUE-106",
			Title: "Done card", Type: domain.TypeTask, Priority: domain.PriorityMedium,
			StoredStatus: domain.StatusDone,
		},
	}

	unprojected := []domain.BoardWorkflowUnprojected{{
		IssueID: "issue-900", IssueDisplayID: "ISSUE-900", Title: "Open backlog item",
		StoredStatus: domain.StatusOpen, Reason: domain.BoardWorkflowReasonNotReady,
		Detail: domain.BoardWorkflowUnprojectedDetail(domain.BoardWorkflowReasonNotReady),
	}}

	return domain.BoardResult{
		GeneratedAt: now,
		StatusCounts: []domain.EffectiveStatusCount{
			{EffectiveStatus: domain.EffectiveStatusReady, Count: 3},
			{EffectiveStatus: domain.EffectiveStatusInProgress, Count: 1},
		},
		ActiveAttempts: []domain.ActiveAttemptSummary{{
			AttemptID: "attempt-work", IssueID: "issue-102", IssueDisplayID: "ISSUE-102",
			IssueTitle: "In progress card", Kind: domain.AttemptKindWork,
			SessionLabel: &label, SessionInstanceKey: &instance, SessionClientName: &client,
			SessionModel: &model, SessionWorktree: &worktree,
			StartedAt: startedAt, LeaseExpiresAt: leaseExpiresAt,
		}},
		AttemptGates: []domain.AttemptGateProgress{{
			AttemptID: "attempt-work", IssueID: "issue-102", IssueDisplayID: "ISSUE-102",
			Gates: domain.WorkContextGateSummary{
				Point: domain.EnforcementPointCompleteWorkToDone, RequirementCount: 2, SatisfiedCount: 1,
			},
		}},
		ActiveReservations: []domain.Reservation{{
			ID: "reservation-1", IssueID: "issue-102", AttemptID: "attempt-work",
			Kind: domain.ResourceKindFile, DisplayValue: "main.go", Status: domain.ReservationStatusActive, Version: 1,
		}},
		BlockedIssues: []domain.IssueProjection{{
			Issue: domain.Issue{
				ID: "issue-105", DisplayID: "ISSUE-105", Title: "Decision required card",
				Status: domain.StatusBlocked, BlockedReason: strPtr("blocked review outcome"),
			},
			EffectiveStatus: domain.EffectiveStatusBlocked,
		}},
		ReviewRequests: []domain.ReviewRequest{{
			ID: "review-open", IssueID: "issue-103", Status: reviewOpen,
			TargetIssueVersion: 4, CreatedAt: requestedAt,
		}},
		PlanningGraph: domain.GraphResult{
			Nodes: []domain.IssueProjection{{
				Issue:           domain.Issue{ID: "issue-101", DisplayID: "ISSUE-101", Title: "Ready card", Status: domain.StatusReady},
				EffectiveStatus: domain.EffectiveStatusReady,
			}},
			Edges: []domain.GraphEdge{}, EntryPoints: []string{}, BlockingNodes: []string{},
			Summary: domain.GraphSummary{NodeCount: 1, EdgeCount: 0, EntryPointCount: 1, BlockingNodeCount: 0},
		},
		Truncation: domain.BoardTruncation{BlockedIssues: true, ReviewRequests: true},
		Workflow:   domain.NewBoardWorkflowProjection(cards, unprojected, domain.BoardWorkflowTruncation{ReviewRequests: true}),
	}
}

// boardWorkflowRenderers lists the two page renderers that share the Kanban
// section template.
var boardWorkflowRenderers = []struct {
	name   string
	render func(domain.BoardResult) (string, error)
}{
	{name: "static", render: renderBoardHTML},
	{name: "served", render: func(result domain.BoardResult) (string, error) {
		return renderServedBoardHTMLWithSearchState(result, servedBoardSearchState{})
	}},
}

// kanbanHeading is the markup that starts one rendered column heading, so a
// test pins the heading rather than any incidental occurrence of its title.
func kanbanHeading(title string, count int) string {
	return `<h3 class="kanban-column-heading">` + title + ` <span class="kanban-count">` + strconv.Itoa(count) + `</span></h3>`
}

// kanbanCardFor returns the rendered <li class="kanban-card"> segment for one
// issue label, so a test can assert on a single card without over-matching
// against the rest of the page.
func kanbanCardFor(t *testing.T, page, issueLabel string) string {
	t.Helper()
	marker := `data-issue="` + issueLabel + `"`
	index := strings.Index(page, marker)
	if index < 0 {
		t.Fatalf("kanban card marker %q not found in page", marker)
	}
	cardStart := strings.LastIndex(page[:index], `<li class="kanban-card"`)
	if cardStart < 0 {
		t.Fatalf("no <li class=\"kanban-card\" before marker %q", marker)
	}
	rest := page[index:]
	cardEnd := len(page)
	if nextCard := strings.Index(rest, `<li class="kanban-card"`); nextCard >= 0 {
		cardEnd = index + nextCard
	} else if listEnd := strings.Index(rest, "\n</ul>"); listEnd >= 0 {
		cardEnd = index + listEnd
	}
	return page[cardStart:cardEnd]
}

func workflowDomainCardByIssue(t *testing.T, workflow domain.BoardWorkflowProjection, issueDisplayID string) domain.BoardWorkflowCard {
	t.Helper()
	for _, card := range workflow.Cards {
		if card.IssueDisplayID == issueDisplayID {
			return card
		}
	}
	t.Fatalf("workflow card for %q not found in projection", issueDisplayID)
	return domain.BoardWorkflowCard{}
}

func workflowCardByIssue(t *testing.T, response BoardResponse, issueDisplayID string) BoardWorkflowCard {
	t.Helper()
	for _, card := range response.Workflow.Cards {
		if card.IssueDisplayID == issueDisplayID {
			return card
		}
	}
	t.Fatalf("workflow card for %q not found in CLI projection", issueDisplayID)
	return BoardWorkflowCard{}
}

// TestBoardWorkflowKanbanRendersEveryColumnAndCard pins that both the offline
// snapshot and the served page render the projection as a six-column Kanban:
// every column heading is present, every card shows its issue label and title,
// and the rendered card count matches the projection. The empty-column case is
// asserted separately because a column with no cards must still show its
// heading and a placeholder rather than disappearing from the board.
func TestBoardWorkflowKanbanRendersEveryColumnAndCard(t *testing.T) {
	result := boardWorkflowFixture()

	for _, renderer := range boardWorkflowRenderers {
		t.Run(renderer.name, func(t *testing.T) {
			page, err := renderer.render(result)
			if err != nil {
				t.Fatalf("%s render error = %v", renderer.name, err)
			}
			for _, column := range domain.BoardWorkflowColumns {
				heading := kanbanHeading(column.Title(), 1)
				if !strings.Contains(page, heading) {
					t.Fatalf("%s page is missing column heading %q", renderer.name, heading)
				}
			}
			for _, card := range result.Workflow.Cards {
				if !strings.Contains(page, `data-issue="`+card.IssueDisplayID+`"`) {
					t.Fatalf("%s page is missing card label %q", renderer.name, card.IssueDisplayID)
				}
				if !strings.Contains(page, card.Title) {
					t.Fatalf("%s page is missing card title %q", renderer.name, card.Title)
				}
			}
			if got := strings.Count(page, `class="kanban-card"`); got != len(result.Workflow.Cards) {
				t.Fatalf("%s page kanban-card count = %d, want %d", renderer.name, got, len(result.Workflow.Cards))
			}
			if !strings.Contains(page, "ISSUE-900") {
				t.Fatalf("%s page is missing the not-projected issue", renderer.name)
			}
		})
	}

	t.Run("empty columns keep their heading and placeholder", func(t *testing.T) {
		rank := int64(1)
		onlyReady := domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{{
			Column: domain.BoardWorkflowColumnReady, IssueID: "issue-101", IssueDisplayID: "ISSUE-101",
			Title: "Ready card", Type: domain.TypeTask, Priority: domain.PriorityMedium,
			StoredStatus: domain.StatusReady, ReadyRank: &rank, IsClaimable: true,
		}}, nil, domain.BoardWorkflowTruncation{})
		emptyResult := boardWorkflowFixture()
		emptyResult.Workflow = onlyReady

		for _, renderer := range boardWorkflowRenderers {
			page, err := renderer.render(emptyResult)
			if err != nil {
				t.Fatalf("%s render error = %v", renderer.name, err)
			}
			for _, column := range domain.BoardWorkflowColumns {
				count := 0
				if column == domain.BoardWorkflowColumnReady {
					count = 1
				}
				heading := kanbanHeading(column.Title(), count)
				if !strings.Contains(page, heading) {
					t.Fatalf("%s page is missing empty-column heading %q", renderer.name, heading)
				}
			}
			if got := strings.Count(page, "No cards."); got != 5 {
				t.Fatalf("%s page empty-column placeholder count = %d, want 5", renderer.name, got)
			}
		}
	})
}

// TestBoardWorkflowInProgressCardShowsSessionRuntime pins that the Kanban card
// carries the same runtime attribution as the rest of the board: all five
// executor fields and the lease expiry, with the role label distinguishing a
// work attempt ("Developer") from a review attempt ("Verifier").
func TestBoardWorkflowInProgressCardShowsSessionRuntime(t *testing.T) {
	result := boardWorkflowFixture()
	page, err := renderServedBoardHTML(result)
	if err != nil {
		t.Fatalf("renderServedBoardHTML: %v", err)
	}

	card := workflowDomainCardByIssue(t, result.Workflow, "ISSUE-102")
	workCard := kanbanCardFor(t, page, "ISSUE-102")
	for _, want := range []string{"Developer", "Luna", "worker-1", "Codex CLI", "gpt-5", "/tmp/wt/ISSUE-102"} {
		if !strings.Contains(workCard, want) {
			t.Fatalf("IN PROGRESS card is missing %q: %s", want, workCard)
		}
	}
	if card.LeaseExpiresAt == nil {
		t.Fatal("fixture IN PROGRESS card has no lease expiry")
	}
	if lease := card.LeaseExpiresAt.UTC().Format(time.RFC3339); !strings.Contains(workCard, lease) {
		t.Fatalf("IN PROGRESS card is missing lease expiry %q: %s", lease, workCard)
	}
	if !strings.Contains(workCard, "commit <code>abc1234</code>") {
		t.Fatalf("IN PROGRESS card is missing its recorded delivery: %s", workCard)
	}

	reviewCard := kanbanCardFor(t, page, "ISSUE-103")
	if !strings.Contains(reviewCard, "Verifier") {
		t.Fatalf("review attempt card role = %s, want Verifier", reviewCard)
	}
	if strings.Contains(reviewCard, "Developer") {
		t.Fatalf("review attempt card rendered a developer role: %s", reviewCard)
	}
}

// TestBoardWorkflowDegradesWithoutAttemptOrReview pins that absence is
// rendered honestly: a card with no attempt and no review names neither a
// developer nor a verifier and invents no delivery value, while a card whose
// attempt exists but whose session metadata is missing degrades each field to
// the em dash instead of a blank or a guessed value.
func TestBoardWorkflowDegradesWithoutAttemptOrReview(t *testing.T) {
	result := boardWorkflowFixture()
	page, err := renderServedBoardHTML(result)
	if err != nil {
		t.Fatalf("renderServedBoardHTML: %v", err)
	}

	unattributed := kanbanCardFor(t, page, "ISSUE-101")
	for _, forbidden := range []string{"Developer", "Verifier", "Delivery", "commit"} {
		if strings.Contains(unattributed, forbidden) {
			t.Fatalf("card with no attempt, review, or delivery rendered %q: %s", forbidden, unattributed)
		}
	}

	sparseResult := result
	sparseResult.Workflow = domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{{
		Column: domain.BoardWorkflowColumnInProgress, IssueID: "issue-201", IssueDisplayID: "ISSUE-201",
		Title: "Claimed without a session", Type: domain.TypeTask, Priority: domain.PriorityMedium,
		StoredStatus: domain.StatusReady,
		AttemptID:    "attempt-sessionless", AttemptKind: domain.AttemptKindWork,
	}}, nil, domain.BoardWorkflowTruncation{})
	sparsePage, err := renderServedBoardHTML(sparseResult)
	if err != nil {
		t.Fatalf("renderServedBoardHTML (session-less): %v", err)
	}
	sparseCard := kanbanCardFor(t, sparsePage, "ISSUE-201")
	if got := strings.Count(sparseCard, "—"); got != 5 {
		t.Fatalf("session-less attempt card placeholders = %d, want 5: %s", got, sparseCard)
	}
	if !strings.Contains(sparseCard, "Developer") {
		t.Fatalf("work attempt card missing its Developer role: %s", sparseCard)
	}
}

// TestBoardWorkflowCLIJSONProjectionIsAdditive pins that
// boardResponseFromDomain carries the workflow faithfully while leaving the
// pre-existing board contract untouched: all six columns with counts, one card
// per issue in the right column, optional fields omitted when absent, and the
// older collections still populated.
func TestBoardWorkflowCLIJSONProjectionIsAdditive(t *testing.T) {
	response := boardResponseFromDomain(boardWorkflowFixture())

	if len(response.Workflow.Columns) != len(domain.BoardWorkflowColumns) {
		t.Fatalf("workflow columns = %d, want %d", len(response.Workflow.Columns), len(domain.BoardWorkflowColumns))
	}
	for index, column := range domain.BoardWorkflowColumns {
		got := response.Workflow.Columns[index]
		if got.Column != string(column) || got.Title != column.Title() || got.Count != 1 {
			t.Fatalf("workflow column[%d] = %#v, want column %q title %q count 1", index, got, column, column.Title())
		}
	}
	if len(response.Workflow.Cards) != 6 {
		t.Fatalf("workflow cards = %d, want 6", len(response.Workflow.Cards))
	}
	wantColumns := map[string]string{
		"ISSUE-101": string(domain.BoardWorkflowColumnReady),
		"ISSUE-102": string(domain.BoardWorkflowColumnInProgress),
		"ISSUE-103": string(domain.BoardWorkflowColumnVerifying),
		"ISSUE-104": string(domain.BoardWorkflowColumnRC),
		"ISSUE-105": string(domain.BoardWorkflowColumnDecisionRequired),
		"ISSUE-106": string(domain.BoardWorkflowColumnDone),
	}
	for issue, wantColumn := range wantColumns {
		if got := workflowCardByIssue(t, response, issue).Column; got != wantColumn {
			t.Fatalf("card %s column = %q, want %q", issue, got, wantColumn)
		}
	}

	work := workflowCardByIssue(t, response, "ISSUE-102")
	if work.ExecutorLabel == nil || *work.ExecutorLabel != "Luna" ||
		work.ExecutorInstanceKey == nil || *work.ExecutorInstanceKey != "worker-1" ||
		work.ExecutorClient == nil || *work.ExecutorClient != "Codex CLI" ||
		work.ExecutorModel == nil || *work.ExecutorModel != "gpt-5" ||
		work.ExecutorWorktree == nil || *work.ExecutorWorktree != "/tmp/wt/ISSUE-102" {
		t.Fatalf("attributed workflow card = %#v", work)
	}
	if work.AttemptKind != string(domain.AttemptKindWork) || len(work.Delivery) != 1 || work.Delivery[0].URI != "abc1234" {
		t.Fatalf("attributed workflow card attempt/delivery = %#v", work)
	}

	ready := workflowCardByIssue(t, response, "ISSUE-101")
	if ready.ExecutorLabel != nil || ready.ExecutorInstanceKey != nil || ready.ExecutorClient != nil ||
		ready.ExecutorModel != nil || ready.ExecutorWorktree != nil || ready.ReviewRequestID != nil ||
		len(ready.Delivery) != 0 {
		t.Fatalf("card without attempt, review, or delivery = %#v", ready)
	}
	if ready.ReadyRank == nil || *ready.ReadyRank != 3 {
		t.Fatalf("READY card ready_rank = %v, want 3", ready.ReadyRank)
	}
	encoded, err := json.Marshal(ready)
	if err != nil {
		t.Fatalf("marshal workflow card: %v", err)
	}
	for _, key := range []string{"executor_label", "executor_instance_key", "executor_client", "executor_model", "executor_worktree", "review_request_id", "delivery"} {
		if strings.Contains(string(encoded), `"`+key+`"`) {
			t.Fatalf("card without optional data emitted %q: %s", key, encoded)
		}
	}

	// The pre-existing projection keeps its own fields and their values.
	if len(response.StatusCounts) != 2 || response.StatusCounts[0].EffectiveStatus != "ready" || response.StatusCounts[0].Count != 3 {
		t.Fatalf("status_counts = %#v", response.StatusCounts)
	}
	if len(response.ActiveAttempts) != 1 || response.ActiveAttempts[0].AttemptID != "attempt-work" {
		t.Fatalf("active_attempts = %#v", response.ActiveAttempts)
	}
	if len(response.AttemptGates) != 1 || response.AttemptGates[0].AttemptID != "attempt-work" {
		t.Fatalf("attempt_gates = %#v", response.AttemptGates)
	}
	if len(response.BlockedIssues) != 1 || response.BlockedIssues[0].DisplayID != "ISSUE-105" {
		t.Fatalf("blocked_issues = %#v", response.BlockedIssues)
	}
	if len(response.ReviewRequests) != 1 || response.ReviewRequests[0].ID != "review-open" {
		t.Fatalf("review_requests = %#v", response.ReviewRequests)
	}
	if response.PlanningGraph.Summary.NodeCount != 1 || response.PlanningGraph.RetainedNodeCount != 1 {
		t.Fatalf("planning_graph = %#v", response.PlanningGraph)
	}
	if !response.Truncation.BlockedIssues || !response.Truncation.ReviewRequests {
		t.Fatalf("truncation = %#v", response.Truncation)
	}

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal board response: %v", err)
	}
	for _, key := range []string{"workflow", "status_counts", "active_attempts", "blocked_issues", "review_requests", "planning_graph", "truncation"} {
		if !strings.Contains(string(payload), `"`+key+`"`) {
			t.Fatalf("board JSON is missing %q", key)
		}
	}
}

// TestBoardWorkflowCLITableHasWorkflowSection pins that the real board table
// carries a workflow section with a keyable header row, one card row per
// projected issue, and the not-projected section, while every pre-existing
// section and its own header row keeps working.
func TestBoardWorkflowCLITableHasWorkflowSection(t *testing.T) {
	result := boardWorkflowFixture()

	var stdout bytes.Buffer
	cli := New(Services{}, &stdout, nil, nil, nil)
	if err := cli.writeBoardTable(result); err != nil {
		t.Fatalf("writeBoardTable() error = %v", err)
	}
	table := stdout.String()

	for _, section := range []string{"\nstatus_counts\n", "\nworkflow\n", "\nworkflow_cards\n", "\nworkflow_unprojected\n", "\nactive_attempts\n", "\nactive_reservations\n", "\nblocked_issues\n", "\nreview_requests\n", "\nplanning_graph\n"} {
		if !strings.Contains(table, section) {
			t.Fatalf("board table is missing section %q:\n%s", section, table)
		}
	}
	columnHeader := "column\ttitle\tcount"
	if !strings.Contains(table, columnHeader) {
		t.Fatalf("board table is missing workflow column header %q:\n%s", columnHeader, table)
	}
	cardHeader := "column\tissue\ttitle\tpriority\tready_rank\tattempt\texecutor\tclient\tmodel\tworktree\treview_status\tchanges_requested\tcommit"
	if !strings.Contains(table, cardHeader) {
		t.Fatalf("board table is missing workflow card header %q:\n%s", cardHeader, table)
	}
	wantRow := "in_progress\tISSUE-102\tIn progress card\thigh\t\twork\tLuna\tCodex CLI\tgpt-5\t/tmp/wt/ISSUE-102\t\t\tcommit abc1234"
	if !strings.Contains(table, wantRow) {
		t.Fatalf("board table is missing workflow card row %q:\n%s", wantRow, table)
	}
	wantReadyRow := "ready\tISSUE-101\tReady card\tmedium\t3\t\t\t\t\t\t\t\t"
	if !strings.Contains(table, wantReadyRow) {
		t.Fatalf("board table is missing READY card row %q:\n%s", wantReadyRow, table)
	}
	if !strings.Contains(table, "ISSUE-900\topen\tnot_ready\tStored open: not yet admitted to the READY queue.") {
		t.Fatalf("board table is missing the not-projected row:\n%s", table)
	}

	// A consumer keys off the header row, so the card rows must follow it.
	sectionAt := strings.Index(table, "\nworkflow_cards\n")
	headerAt := strings.Index(table, cardHeader)
	rowAt := strings.Index(table, wantRow)
	unprojectedAt := strings.Index(table, "\nworkflow_unprojected\n")
	if sectionAt < 0 || headerAt < 0 || rowAt < 0 || unprojectedAt < 0 {
		t.Fatalf("workflow section offsets = section %d, header %d, row %d, unprojected %d", sectionAt, headerAt, rowAt, unprojectedAt)
	}
	if headerAt < sectionAt || rowAt < headerAt || rowAt > unprojectedAt {
		t.Fatalf("workflow card row at %d, want after header %d (section %d) and before unprojected %d", rowAt, headerAt, sectionAt, unprojectedAt)
	}
}

// boardWorkflowMoveBoard returns a board whose only varying field is the
// column of its single workflow card, so an ETag difference between two of
// them can only come from the card moving.
func boardWorkflowMoveBoard(column domain.BoardWorkflowColumn) domain.BoardResult {
	card := domain.BoardWorkflowCard{
		Column: column, IssueID: "issue-1", IssueDisplayID: "ISSUE-1", Title: "One movable card",
		Type: domain.TypeTask, Priority: domain.PriorityMedium, StoredStatus: domain.StatusReady, IsClaimable: true,
	}
	return domain.BoardResult{
		GeneratedAt:    time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC),
		StatusCounts:   []domain.EffectiveStatusCount{{EffectiveStatus: domain.EffectiveStatusReady, Count: 1}},
		ActiveAttempts: []domain.ActiveAttemptSummary{}, AttemptGates: []domain.AttemptGateProgress{},
		ActiveReservations: []domain.Reservation{}, BlockedIssues: []domain.IssueProjection{},
		ReviewRequests: []domain.ReviewRequest{},
		PlanningGraph: domain.GraphResult{
			Nodes: []domain.IssueProjection{}, Edges: []domain.GraphEdge{},
			EntryPoints: []string{}, BlockingNodes: []string{},
		},
		Workflow: domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{card}, []domain.BoardWorkflowUnprojected{}, domain.BoardWorkflowTruncation{}),
	}
}

// TestBoardWorkflowETagTracksCardColumnMove pins that the semantic ETag is
// both stable for identical boards and sensitive to a card moving between
// columns, so a polling board refreshes when the Kanban changes even though
// nothing else did.
func TestBoardWorkflowETagTracksCardColumnMove(t *testing.T) {
	ready := boardWorkflowMoveBoard(domain.BoardWorkflowColumnReady)
	if baseline := semanticBoardETag(ready); baseline == "" {
		t.Fatal("semanticBoardETag returned an empty ETag")
	}
	if semanticBoardETag(ready) != semanticBoardETag(ready) {
		t.Fatal("semanticBoardETag is not stable for identical boards")
	}

	moved := boardWorkflowMoveBoard(domain.BoardWorkflowColumnInProgress)
	if semanticBoardETag(ready) == semanticBoardETag(moved) {
		t.Fatal("semanticBoardETag ignored a card moving from ready to in_progress")
	}

	// Reverse the move to prove the difference is the column, not an
	// incidental ordering or allocation difference between two calls.
	if semanticBoardETag(moved) == semanticBoardETag(boardWorkflowMoveBoard(domain.BoardWorkflowColumnRC)) {
		t.Fatal("semanticBoardETag ignored a card moving from in_progress to rc")
	}
}
