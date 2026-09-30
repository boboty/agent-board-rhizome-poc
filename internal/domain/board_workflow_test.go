package domain_test

import (
	"reflect"
	"testing"
	"time"

	"rhizome-mcp/internal/domain"
)

func workflowIssue(status domain.Status) domain.Issue {
	return domain.Issue{ID: "issue-1", DisplayID: "ISSUE-1", Type: domain.TypeTask, Title: "Work", Status: status, Priority: domain.PriorityMedium}
}

func workflowWorkAttempt() *domain.ActiveAttemptSummary {
	return &domain.ActiveAttemptSummary{AttemptID: "attempt-1", IssueID: "issue-1", IssueDisplayID: "ISSUE-1", Kind: domain.AttemptKindWork}
}

func workflowReviewAttempt() *domain.ActiveAttemptSummary {
	return &domain.ActiveAttemptSummary{AttemptID: "attempt-1", IssueID: "issue-1", IssueDisplayID: "ISSUE-1", Kind: domain.AttemptKindReview}
}

func workflowReview(status domain.ReviewRequestStatus) *domain.ReviewRequest {
	return &domain.ReviewRequest{ID: "review-1", IssueID: "issue-1", Status: status, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
}

// TestDeriveBoardWorkflowPlacementMapsRealStateToColumn pins the whole mapping
// table. Every case is a state the domain can actually produce, and the
// expected column is one of the four task-level states the Agent Board shows.
func TestDeriveBoardWorkflowPlacementMapsRealStateToColumn(t *testing.T) {
	archived := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	archivedIssue := workflowIssue(domain.StatusReady)
	archivedIssue.ArchivedAt = &archived

	tests := []struct {
		name       string
		issue      domain.Issue
		attempt    *domain.ActiveAttemptSummary
		wantColumn domain.BoardWorkflowColumn
		wantReason string
	}{
		{name: "ready and unclaimed is READY", issue: workflowIssue(domain.StatusReady), wantColumn: domain.BoardWorkflowColumnReady},
		{name: "ready with an active work attempt is IN PROGRESS", issue: workflowIssue(domain.StatusReady), attempt: workflowWorkAttempt(), wantColumn: domain.BoardWorkflowColumnInProgress},
		{name: "ready with an active review attempt is still IN PROGRESS", issue: workflowIssue(domain.StatusReady), attempt: workflowReviewAttempt(), wantColumn: domain.BoardWorkflowColumnInProgress},
		{name: "stored review is IN PROGRESS", issue: workflowIssue(domain.StatusReview), wantColumn: domain.BoardWorkflowColumnInProgress},
		{name: "stored review with an active review attempt stays IN PROGRESS", issue: workflowIssue(domain.StatusReview), attempt: workflowReviewAttempt(), wantColumn: domain.BoardWorkflowColumnInProgress},
		{name: "stored review with an active work attempt stays IN PROGRESS", issue: workflowIssue(domain.StatusReview), attempt: workflowWorkAttempt(), wantColumn: domain.BoardWorkflowColumnInProgress},
		{name: "blocked is BLOCKED", issue: workflowIssue(domain.StatusBlocked), wantColumn: domain.BoardWorkflowColumnBlocked},
		{name: "an active attempt outranks a stored block", issue: workflowIssue(domain.StatusBlocked), attempt: workflowWorkAttempt(), wantColumn: domain.BoardWorkflowColumnInProgress},
		{name: "done is DONE", issue: workflowIssue(domain.StatusDone), wantColumn: domain.BoardWorkflowColumnDone},
		{name: "done wins over a lingering attempt", issue: workflowIssue(domain.StatusDone), attempt: workflowReviewAttempt(), wantColumn: domain.BoardWorkflowColumnDone},
		{name: "cancelled is not on the board", issue: workflowIssue(domain.StatusCancelled), wantReason: domain.BoardWorkflowReasonCancelled},
		{name: "open is not yet READY", issue: workflowIssue(domain.StatusOpen), wantReason: domain.BoardWorkflowReasonNotReady},
		{name: "an archived issue is not on the board", issue: archivedIssue, wantReason: domain.BoardWorkflowReasonArchived},
		{name: "an unsupported stored status degrades", issue: workflowIssue(domain.Status("mystery")), wantReason: domain.BoardWorkflowReasonUnknownStatus},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			column, reason := domain.DeriveBoardWorkflowPlacement(domain.BoardWorkflowPlacementInput{
				Issue: test.issue, ActiveAttempt: test.attempt,
			})
			if column != test.wantColumn || reason != test.wantReason {
				t.Fatalf("placement = (%q, %q), want (%q, %q)", column, reason, test.wantColumn, test.wantReason)
			}
		})
	}
}

// TestBoardWorkflowPlacementHasNoReviewInput is the AB-5 structural guarantee
// that review, verification, and changes-requested state cannot move a card
// between task-level columns: the derivation input carries an issue and an
// active attempt, and nothing a review read produces. The states the removed
// VERIFYING / RC / DECISION REQUIRED columns used to hold are pinned here to
// their new task-level homes.
func TestBoardWorkflowPlacementHasNoReviewInput(t *testing.T) {
	// The keys are exactly the fields DeriveBoardWorkflowPlacement may read.
	// Adding a review field here would make the review signal part of the
	// column contract again, which AB-5 forbids.
	input := domain.BoardWorkflowPlacementInput{}
	fields := reflect.TypeOf(input).NumField()
	if fields != 2 {
		t.Fatalf("BoardWorkflowPlacementInput has %d fields, want issue + active attempt only", fields)
	}
	if _, ok := reflect.TypeOf(input).FieldByName("LatestReview"); ok {
		t.Fatal("placement input carries a review signal; reviews must be card detail only")
	}

	// A stored-ready issue is READY even when a review round requested changes:
	// the rework is executable work, not a separate RC column.
	ready, reason := domain.DeriveBoardWorkflowPlacement(domain.BoardWorkflowPlacementInput{Issue: workflowIssue(domain.StatusReady)})
	if ready != domain.BoardWorkflowColumnReady || reason != "" {
		t.Fatalf("stored ready placement = (%q, %q), want READY", ready, reason)
	}
	// A blocked issue is BLOCKED whether the block came from a review decision
	// or from an external condition: the cause is card detail.
	blocked, reason := domain.DeriveBoardWorkflowPlacement(domain.BoardWorkflowPlacementInput{Issue: workflowIssue(domain.StatusBlocked)})
	if blocked != domain.BoardWorkflowColumnBlocked || reason != "" {
		t.Fatalf("stored blocked placement = (%q, %q), want BLOCKED", blocked, reason)
	}
}

// TestDeriveBoardWorkflowPlacementAlwaysPlacesOrExplains is the one-placement
// invariant at the derivation level: for every combination of stored status
// and attempt kind the domain can produce, the issue lands in exactly one
// column or is explained by exactly one reason. It must never be both and
// never neither, because "neither" would drop a task and "both" would let it
// appear twice.
func TestDeriveBoardWorkflowPlacementAlwaysPlacesOrExplains(t *testing.T) {
	statuses := []domain.Status{domain.StatusOpen, domain.StatusReady, domain.StatusBlocked, domain.StatusReview, domain.StatusDone, domain.StatusCancelled, domain.Status("mystery")}
	attempts := []*domain.ActiveAttemptSummary{nil, workflowWorkAttempt(), workflowReviewAttempt()}
	combinations := 0
	for _, status := range statuses {
		for _, attempt := range attempts {
			combinations++
			column, reason := domain.DeriveBoardWorkflowPlacement(domain.BoardWorkflowPlacementInput{
				Issue: workflowIssue(status), ActiveAttempt: attempt,
			})
			placed := column != ""
			explained := reason != ""
			if placed == explained {
				t.Fatalf("status=%q attempt=%v: column=%q reason=%q, want exactly one of them set",
					status, attempt != nil, column, reason)
			}
			if placed && !column.Valid() {
				t.Fatalf("status=%q: column %q is not a supported workflow column", status, column)
			}
			// The four task-level columns are the whole board: no placement can
			// produce the retired process-phase states.
			for _, retired := range []domain.BoardWorkflowColumn{"verifying", "rc", "decision_required"} {
				if column == retired {
					t.Fatalf("status=%q: placement produced retired column %q", status, retired)
				}
			}
		}
	}
	if combinations == 0 {
		t.Fatal("no combinations exercised")
	}
}

func TestBoardWorkflowColumnTitlesCoverEveryColumn(t *testing.T) {
	want := map[domain.BoardWorkflowColumn]string{
		domain.BoardWorkflowColumnReady:      "READY",
		domain.BoardWorkflowColumnInProgress: "IN PROGRESS",
		domain.BoardWorkflowColumnDone:       "DONE",
		domain.BoardWorkflowColumnBlocked:    "BLOCKED",
	}
	if len(domain.BoardWorkflowColumns) != 4 {
		t.Fatalf("BoardWorkflowColumns = %v, want exactly the four task-level columns", domain.BoardWorkflowColumns)
	}
	if len(domain.BoardWorkflowColumns) != len(want) {
		t.Fatalf("BoardWorkflowColumns = %v, want %d columns", domain.BoardWorkflowColumns, len(want))
	}
	for _, column := range domain.BoardWorkflowColumns {
		title, ok := want[column]
		if !ok {
			t.Fatalf("unexpected column %q", column)
		}
		if got := column.Title(); got != title {
			t.Fatalf("Title() for %q = %q, want %q", column, got, title)
		}
		if !column.Valid() {
			t.Fatalf("column %q reports invalid", column)
		}
	}
	if domain.BoardWorkflowColumn("bogus").Valid() {
		t.Fatal("bogus column reports valid")
	}
}

func workflowCard(column domain.BoardWorkflowColumn, displayID string, priority domain.Priority, rank *int64) domain.BoardWorkflowCard {
	return domain.BoardWorkflowCard{
		Column: column, IssueID: "id-" + displayID, IssueDisplayID: displayID,
		Title: "Title " + displayID, Priority: priority, ReadyRank: rank,
	}
}

// TestNewBoardWorkflowProjectionListsEveryColumnAndCountsCards checks that a
// UI never has to infer a column: all four are always present, in board order,
// with counts that match the cards actually handed in.
func TestNewBoardWorkflowProjectionListsEveryColumnAndCountsCards(t *testing.T) {
	rank := int64(4)
	projection := domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-1", domain.PriorityHigh, &rank),
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-2", domain.PriorityLow, nil),
		workflowCard(domain.BoardWorkflowColumnDone, "ISSUE-3", domain.PriorityLow, nil),
	}, nil, domain.BoardWorkflowTruncation{})

	if len(projection.Columns) != len(domain.BoardWorkflowColumns) {
		t.Fatalf("Columns = %#v, want one per workflow column", projection.Columns)
	}
	for index, column := range domain.BoardWorkflowColumns {
		if projection.Columns[index].Column != column {
			t.Fatalf("Columns[%d] = %q, want %q", index, projection.Columns[index].Column, column)
		}
		if projection.Columns[index].Title != column.Title() {
			t.Fatalf("Columns[%d].Title = %q, want %q", index, projection.Columns[index].Title, column.Title())
		}
	}
	wantCounts := map[domain.BoardWorkflowColumn]int{
		domain.BoardWorkflowColumnReady: 2,
		domain.BoardWorkflowColumnDone:  1,
	}
	for _, column := range projection.Columns {
		if column.Count != wantCounts[column.Column] {
			t.Fatalf("column %q count = %d, want %d", column.Column, column.Count, wantCounts[column.Column])
		}
	}
	if projection.Cards == nil || projection.Unprojected == nil {
		t.Fatal("Cards and Unprojected must be non-nil so a template ranges over them safely")
	}
}

// TestNewBoardWorkflowProjectionOrdersReadyCardsByRankThenPriority pins the
// READY queue order: explicit rank first (ascending), unranked after ranked,
// then priority, then display id for a stable tie-break.
func TestNewBoardWorkflowProjectionOrdersReadyCardsByRankThenPriority(t *testing.T) {
	rank2, rank9 := int64(2), int64(9)
	projection := domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-E", domain.PriorityLow, nil),
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-D", domain.PriorityMedium, nil),
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-C", domain.PriorityLow, &rank9),
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-B", domain.PriorityCritical, &rank2),
	}, nil, domain.BoardWorkflowTruncation{})

	want := []string{"ISSUE-B", "ISSUE-C", "ISSUE-D", "ISSUE-E"}
	got := make([]string, 0, len(projection.Cards))
	for _, card := range projection.Cards {
		got = append(got, card.IssueDisplayID)
	}
	if len(got) != len(want) {
		t.Fatalf("card order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("card order = %v, want %v", got, want)
		}
	}
}

// TestNewBoardWorkflowProjectionKeepsReadyRankOnlyOnReadyCards guards the
// card contract: READY rank is READY-queue metadata and must not leak onto
// cards in other columns, where it means nothing.
func TestNewBoardWorkflowProjectionKeepsReadyRankOnlyOnReadyCards(t *testing.T) {
	rank := int64(3)
	projection := domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{
		workflowCard(domain.BoardWorkflowColumnReady, "ISSUE-1", domain.PriorityLow, &rank),
		workflowCard(domain.BoardWorkflowColumnDone, "ISSUE-2", domain.PriorityLow, nil),
	}, nil, domain.BoardWorkflowTruncation{})
	for _, card := range projection.Cards {
		if card.Column == domain.BoardWorkflowColumnReady && card.ReadyRank == nil {
			t.Fatalf("READY card %q lost its rank", card.IssueDisplayID)
		}
		if card.Column != domain.BoardWorkflowColumnReady && card.ReadyRank != nil {
			t.Fatalf("non-READY card %q carries ready_rank %d", card.IssueDisplayID, *card.ReadyRank)
		}
	}
}

// TestBoardWorkflowUnprojectedDetailExplainsEveryReason keeps the degraded
// path legible: every reason code the projection can emit has a sentence,
// including the fallback.
func TestBoardWorkflowUnprojectedDetailExplainsEveryReason(t *testing.T) {
	reasons := []string{
		domain.BoardWorkflowReasonArchived,
		domain.BoardWorkflowReasonCancelled,
		domain.BoardWorkflowReasonNotReady,
		domain.BoardWorkflowReasonUnknownStatus,
		"something-new",
	}
	for _, reason := range reasons {
		if detail := domain.BoardWorkflowUnprojectedDetail(reason); detail == "" {
			t.Fatalf("reason %q has no detail sentence", reason)
		}
	}
}

func TestBoardWorkflowTruncationAny(t *testing.T) {
	if (domain.BoardWorkflowTruncation{}).Any() {
		t.Fatal("zero truncation reports truncated")
	}
	for name, truncation := range map[string]domain.BoardWorkflowTruncation{
		"ready":             {Ready: true},
		"review":            {Review: true},
		"blocked":           {Blocked: true},
		"done":              {Done: true},
		"unprojected":       {Unprojected: true},
		"review_requests":   {ReviewRequests: true},
		"delivery_overflow": {DeliveryOverflow: true},
	} {
		if !truncation.Any() {
			t.Fatalf("%s truncation reports not truncated", name)
		}
	}
}
