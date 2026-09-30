package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"rhizome-mcp/internal/domain"
	"rhizome-mcp/internal/ports"
)

// boardStubDeliveryReader is a delivery-reference reader for the board's
// workflow projection. It records which issues were asked for so a test can
// prove the board only reads delivery references for cards that can have one.
type boardStubDeliveryReader struct {
	byIssue   map[string][]domain.Artifact
	err       error
	truncated bool
	called    []string
}

func (reader *boardStubDeliveryReader) IssueDeliveryReferences(_ context.Context, issueID string) ([]domain.Artifact, bool, error) {
	reader.called = append(reader.called, issueID)
	if reader.err != nil {
		return nil, false, reader.err
	}
	return reader.byIssue[issueID], reader.truncated, nil
}

func workflowProjectionIssue(id string, displayID string, status domain.Status, priority domain.Priority) domain.IssueProjection {
	return domain.IssueProjection{
		Issue: domain.Issue{
			ID: id, DisplayID: displayID, Type: domain.TypeTask, Title: "Title " + displayID,
			Status: status, Priority: priority,
		},
		EffectiveStatus: domain.EffectiveStatus(status),
	}
}

func workflowProjectionAttempt(attemptID string, issueID string, displayID string, kind domain.AttemptKind, now time.Time) domain.ActiveAttemptSummary {
	label := "developer-" + displayID
	client := "codex"
	model := "gpt-5"
	worktree := "/worktrees/" + displayID
	instance := "instance-" + displayID
	return domain.ActiveAttemptSummary{
		AttemptID: attemptID, IssueID: issueID, IssueDisplayID: displayID, IssueTitle: "Title " + displayID,
		Kind: kind, SessionLabel: &label, SessionClientName: &client, SessionModel: &model,
		SessionWorktree: &worktree, SessionInstanceKey: &instance,
		StartedAt: now.Add(-time.Minute), LeaseExpiresAt: now.Add(30 * time.Minute),
	}
}

func workflowProjectionReview(id string, issueID string, status domain.ReviewRequestStatus, now time.Time) domain.ReviewRequest {
	return domain.ReviewRequest{ID: id, IssueID: issueID, Status: status, TargetIssueVersion: 2, CreatedAt: now.Add(-time.Hour)}
}

// workflowBoardFixture seeds one project with a real issue in every workflow
// state the board must project, then returns the composed board service.
func workflowBoardFixture(t *testing.T, now time.Time) (*BoardService, *boardStubDeliveryReader) {
	t.Helper()

	ready := []domain.IssueProjection{
		workflowProjectionIssue("issue-10", "ISSUE-10", domain.StatusReady, domain.PriorityHigh),   // active work attempt -> IN PROGRESS
		workflowProjectionIssue("issue-20", "ISSUE-20", domain.StatusReady, domain.PriorityMedium), // changes requested -> RC
		workflowProjectionIssue("issue-21", "ISSUE-21", domain.StatusReady, domain.PriorityMedium), // -> READY
		workflowProjectionIssue("issue-22", "ISSUE-22", domain.StatusReady, domain.PriorityLow),    // -> READY
	}
	rank := int64(4)
	ready[2].ReadyRank = &rank
	review := []domain.IssueProjection{
		workflowProjectionIssue("issue-30", "ISSUE-30", domain.StatusReview, domain.PriorityHigh),   // active review attempt -> VERIFYING
		workflowProjectionIssue("issue-31", "ISSUE-31", domain.StatusReview, domain.PriorityMedium), // -> VERIFYING
	}
	done := []domain.IssueProjection{
		workflowProjectionIssue("issue-40", "ISSUE-40", domain.StatusDone, domain.PriorityLow),
	}
	rest := []domain.IssueProjection{
		workflowProjectionIssue("issue-50", "ISSUE-50", domain.StatusBlocked, domain.PriorityHigh), // blocked review -> DECISION REQUIRED
		workflowProjectionIssue("issue-60", "ISSUE-60", domain.StatusBlocked, domain.PriorityLow),  // external block -> unprojected
		workflowProjectionIssue("issue-70", "ISSUE-70", domain.StatusOpen, domain.PriorityLow),     // not ready -> unprojected
	}

	issueRepo := &boardRecordingIssueRepository{
		countResult: []domain.EffectiveStatusCount{{EffectiveStatus: domain.EffectiveStatusReady, Count: 6}},
		listResult:  domain.IssueList{Items: rest},
		listResultsByStatus: map[domain.Status]domain.IssueList{
			domain.StatusReady:  {Items: ready},
			domain.StatusReview: {Items: review},
			domain.StatusDone:   {Items: done},
		},
		blockedResult: &domain.IssueList{Items: []domain.IssueProjection{rest[0]}},
	}
	attemptRepo := &boardRecordingAttemptRepository{listResult: domain.ActiveAttemptList{Items: []domain.ActiveAttemptSummary{
		workflowProjectionAttempt("attempt-10", "issue-10", "ISSUE-10", domain.AttemptKindWork, now),
		workflowProjectionAttempt("attempt-30", "issue-30", "ISSUE-30", domain.AttemptKindReview, now),
	}}}
	reservationRepo := &boardRecordingReservationRepository{}
	reviewRepo := &boardRecordingReviewRepository{
		listResult: ports.ListReviewRequestsResult{},
		listResultsByStatus: map[domain.ReviewRequestStatus]ports.ListReviewRequestsResult{
			domain.ReviewRequestStatusClaimed: {Items: []domain.ReviewRequest{
				workflowProjectionReview("review-30", "issue-30", domain.ReviewRequestStatusClaimed, now),
			}},
			domain.ReviewRequestStatusChangesRequested: {Items: []domain.ReviewRequest{
				workflowProjectionReview("review-20", "issue-20", domain.ReviewRequestStatusChangesRequested, now),
			}},
			domain.ReviewRequestStatusBlocked: {Items: []domain.ReviewRequest{
				workflowProjectionReview("review-50", "issue-50", domain.ReviewRequestStatusBlocked, now),
			}},
		},
	}
	graphRepo := &boardRecordingGraphRepository{snapshot: domain.GraphSnapshot{}}

	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
	reader := &boardStubDeliveryReader{byIssue: map[string][]domain.Artifact{}}
	service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, reader, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}
	return service, reader
}

// TestBoardServiceWorkflowProjectsRealStateOntoColumns is AC1: after seeding
// ready work, active work, open and active review, changes requested, a
// blocked decision, and done work, every card lands in the expected column.
func TestBoardServiceWorkflowProjectsRealStateOntoColumns(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, _ := workflowBoardFixture(t, now)

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	cardsByColumn := map[domain.BoardWorkflowColumn][]string{}
	for _, card := range result.Workflow.Cards {
		cardsByColumn[card.Column] = append(cardsByColumn[card.Column], card.IssueDisplayID)
	}
	want := map[domain.BoardWorkflowColumn][]string{
		domain.BoardWorkflowColumnReady:            {"ISSUE-21", "ISSUE-22"},
		domain.BoardWorkflowColumnInProgress:       {"ISSUE-10"},
		domain.BoardWorkflowColumnVerifying:        {"ISSUE-30", "ISSUE-31"},
		domain.BoardWorkflowColumnRC:               {"ISSUE-20"},
		domain.BoardWorkflowColumnDecisionRequired: {"ISSUE-50"},
		domain.BoardWorkflowColumnDone:             {"ISSUE-40"},
	}
	for column, wantIssues := range want {
		got := cardsByColumn[column]
		if len(got) != len(wantIssues) {
			t.Fatalf("column %q cards = %v, want %v", column, got, wantIssues)
		}
		seen := map[string]bool{}
		for _, issue := range got {
			seen[issue] = true
		}
		for _, issue := range wantIssues {
			if !seen[issue] {
				t.Fatalf("column %q cards = %v, want %v", column, got, wantIssues)
			}
		}
	}
	for _, column := range result.Workflow.Columns {
		if column.Count != len(cardsByColumn[column.Column]) {
			t.Fatalf("column %q count = %d, want %d", column.Column, column.Count, len(cardsByColumn[column.Column]))
		}
	}
	if len(result.Workflow.Unprojected) != 2 {
		t.Fatalf("Unprojected = %#v, want the two issues no column describes", result.Workflow.Unprojected)
	}
	reasons := map[string]string{}
	for _, item := range result.Workflow.Unprojected {
		reasons[item.IssueDisplayID] = item.Reason
	}
	if reasons["ISSUE-60"] != domain.BoardWorkflowReasonExternallyBlocked || reasons["ISSUE-70"] != domain.BoardWorkflowReasonNotReady {
		t.Fatalf("unprojected reasons = %#v", reasons)
	}
}

// TestBoardServiceWorkflowPlacesEachIssueInExactlyOneColumn is AC2: with one
// project seeded, every issue appears exactly once across all columns and the
// unprojected list. A task can never be in two columns, and never vanish.
func TestBoardServiceWorkflowPlacesEachIssueInExactlyOneColumn(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, _ := workflowBoardFixture(t, now)

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	placements := map[string]int{}
	for _, card := range result.Workflow.Cards {
		placements[card.IssueID]++
	}
	for _, item := range result.Workflow.Unprojected {
		placements[item.IssueID]++
	}
	wantIssues := []string{"issue-10", "issue-20", "issue-21", "issue-22", "issue-30", "issue-31", "issue-40", "issue-50", "issue-60", "issue-70"}
	if len(placements) != len(wantIssues) {
		t.Fatalf("placements = %#v, want one entry per seeded issue", placements)
	}
	for _, issueID := range wantIssues {
		if placements[issueID] != 1 {
			t.Fatalf("issue %q placement count = %d, want exactly 1", issueID, placements[issueID])
		}
	}
}

// TestBoardServiceWorkflowCardShowsExecutorRuntimeMetadata is AC3: the
// IN PROGRESS card carries the claiming session's harness, model, worktree,
// and instance key, and a review attempt is labelled as the verifier.
func TestBoardServiceWorkflowCardShowsExecutorRuntimeMetadata(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, _ := workflowBoardFixture(t, now)

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	var inProgress, verifying *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		switch card.Column {
		case domain.BoardWorkflowColumnInProgress:
			inProgress = &result.Workflow.Cards[index]
		case domain.BoardWorkflowColumnVerifying:
			if card.IssueDisplayID == "ISSUE-30" {
				verifying = &result.Workflow.Cards[index]
			}
		}
	}
	if inProgress == nil || verifying == nil {
		t.Fatalf("missing IN PROGRESS or VERIFYING card: %#v", result.Workflow.Cards)
	}
	if inProgress.AttemptKind != domain.AttemptKindWork {
		t.Fatalf("IN PROGRESS attempt kind = %q, want %q", inProgress.AttemptKind, domain.AttemptKindWork)
	}
	for name, got := range map[string]*string{
		"executor_label":        inProgress.ExecutorLabel,
		"executor_client":       inProgress.ExecutorClient,
		"executor_model":        inProgress.ExecutorModel,
		"executor_worktree":     inProgress.ExecutorWorktree,
		"executor_instance_key": inProgress.ExecutorInstanceKey,
	} {
		if got == nil || *got == "" {
			t.Fatalf("IN PROGRESS card %s = %v, want the claiming session's value", name, got)
		}
	}
	if inProgress.LeaseExpiresAt == nil || inProgress.AttemptStartedAt == nil {
		t.Fatalf("IN PROGRESS card lease/started = %v / %v, want both set", inProgress.LeaseExpiresAt, inProgress.AttemptStartedAt)
	}
	if verifying.AttemptKind != domain.AttemptKindReview {
		t.Fatalf("VERIFYING attempt kind = %q, want %q", verifying.AttemptKind, domain.AttemptKindReview)
	}
	if verifying.ReviewStatus == nil || *verifying.ReviewStatus != domain.ReviewRequestStatusClaimed {
		t.Fatalf("VERIFYING review status = %v, want claimed", verifying.ReviewStatus)
	}

	var rc, decision *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		switch card.Column {
		case domain.BoardWorkflowColumnRC:
			rc = &result.Workflow.Cards[index]
		case domain.BoardWorkflowColumnDecisionRequired:
			decision = &result.Workflow.Cards[index]
		}
	}
	if rc == nil || rc.ReviewStatus == nil || *rc.ReviewStatus != domain.ReviewRequestStatusChangesRequested {
		t.Fatalf("RC review status = %v, want changes_requested", rc)
	}
	if rc.ChangesRequestedCount != 1 {
		t.Fatalf("RC changes_requested_count = %d, want 1", rc.ChangesRequestedCount)
	}
	if rc.AttemptID != "" {
		t.Fatalf("RC card attempt = %q, want no active attempt", rc.AttemptID)
	}
	if decision == nil || decision.ReviewStatus == nil || *decision.ReviewStatus != domain.ReviewRequestStatusBlocked {
		t.Fatalf("DECISION REQUIRED review status = %v, want blocked", decision)
	}
}

// TestBoardServiceWorkflowReadyCardCarriesQueueRank checks the READY column's
// own field: the explicit queue position is on the card, and only there.
func TestBoardServiceWorkflowReadyCardCarriesQueueRank(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, _ := workflowBoardFixture(t, now)

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}
	ranked := 0
	for _, card := range result.Workflow.Cards {
		if card.Column == domain.BoardWorkflowColumnReady {
			if card.IssueDisplayID == "ISSUE-21" {
				if card.ReadyRank == nil || *card.ReadyRank != 4 {
					t.Fatalf("ISSUE-21 ready_rank = %v, want 4", card.ReadyRank)
				}
				ranked++
			}
			if card.IssueDisplayID == "ISSUE-22" && card.ReadyRank != nil {
				t.Fatalf("ISSUE-22 ready_rank = %v, want nil", *card.ReadyRank)
			}
		}
		if card.Column != domain.BoardWorkflowColumnReady && card.ReadyRank != nil {
			t.Fatalf("card %q in column %q carries ready_rank", card.IssueDisplayID, card.Column)
		}
	}
	if ranked != 1 {
		t.Fatalf("ranked READY cards = %d, want 1", ranked)
	}
}

// TestBoardServiceWorkflowCardShowsDeliveryReferences checks the "show it when
// it exists" half of the delivery contract, and that non-delivery artifacts
// (a design note) are not presented as one.
func TestBoardServiceWorkflowCardShowsDeliveryReferences(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, reader := workflowBoardFixture(t, now)
	title := "feat: agent board kanban"
	reader.byIssue["issue-40"] = []domain.Artifact{
		{ID: "artifact-1", IssueID: "issue-40", Type: domain.ArtifactTypeCommit, URI: "abc1234", Title: &title},
		{ID: "artifact-2", IssueID: "issue-40", Type: domain.ArtifactTypeFile, URI: "/tmp/notes.md"},
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	var done *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		if card.Column == domain.BoardWorkflowColumnDone {
			done = &result.Workflow.Cards[index]
		}
	}
	if done == nil {
		t.Fatal("no DONE card")
	}
	if len(done.Delivery) != 1 || done.Delivery[0].Type != domain.ArtifactTypeCommit || done.Delivery[0].URI != "abc1234" {
		t.Fatalf("DONE card delivery = %#v, want the single commit reference", done.Delivery)
	}
	if done.Delivery[0].Title == nil || *done.Delivery[0].Title != title {
		t.Fatalf("DONE card delivery title = %#v, want %q", done.Delivery[0].Title, title)
	}
	for _, issueID := range reader.called {
		if issueID == "issue-21" || issueID == "issue-22" {
			t.Fatalf("delivery reader was asked for READY issue %q, which cannot have a delivery yet", issueID)
		}
	}
}

// TestBoardServiceWorkflowDeliveryReaderFailureDegradesTheCard keeps the read
// honest: a delivery lookup error leaves the card on the board without its
// references and is reported through truncation.delivery_unavailable, rather
// than failing the whole projection or silently dropping the reference.
func TestBoardServiceWorkflowDeliveryReaderFailureDegradesTheCard(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, reader := workflowBoardFixture(t, now)
	reader.err = errors.New("artifact read failed")

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v, want a degraded board", err)
	}
	if !result.Workflow.Truncation.DeliveryUnavailable {
		t.Fatal("Truncation.DeliveryUnavailable = false, want true")
	}
	if result.Workflow.Truncation.DeliveryOverflow {
		t.Fatal("Truncation.DeliveryOverflow = true, want false for a plain read failure")
	}
	if !result.Workflow.Truncation.Any() {
		t.Fatal("Truncation.Any() = false, want true")
	}
	for _, card := range result.Workflow.Cards {
		if card.Column != domain.BoardWorkflowColumnReady && len(card.Delivery) != 0 {
			t.Fatalf("card %q kept delivery %#v despite the read failure", card.IssueDisplayID, card.Delivery)
		}
	}
}

// TestBoardServiceWorkflowDeliveryReadTruncationSetsOverflow covers the other
// half of a card showing fewer references than its issue has: the artifact
// read itself was cut before the per-card cap ran.
func TestBoardServiceWorkflowDeliveryReadTruncationSetsOverflow(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, reader := workflowBoardFixture(t, now)
	reader.truncated = true
	reader.byIssue["issue-40"] = []domain.Artifact{
		{ID: "artifact-1", IssueID: "issue-40", Type: domain.ArtifactTypeCommit, URI: "abc1234"},
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}
	if !result.Workflow.Truncation.DeliveryOverflow {
		t.Fatal("Truncation.DeliveryOverflow = false, want true when the artifact read was truncated")
	}
	if result.Workflow.Truncation.DeliveryUnavailable {
		t.Fatal("Truncation.DeliveryUnavailable = true, want false for a truncated read")
	}
}

// TestBoardServiceWorkflowTruncationFollowsTheBoundedReads proves the
// projection reports a cut rather than presenting a lower bound as a total.
func TestBoardServiceWorkflowTruncationFollowsTheBoundedReads(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	// A fixture whose every contributing read is cut at the collection limit.
	ready := []domain.IssueProjection{workflowProjectionIssue("issue-21", "ISSUE-21", domain.StatusReady, domain.PriorityLow)}
	reviewOf := func() []domain.IssueProjection {
		return []domain.IssueProjection{workflowProjectionIssue("issue-31", "ISSUE-31", domain.StatusReview, domain.PriorityMedium)}
	}
	doneOf := func() []domain.IssueProjection {
		return []domain.IssueProjection{workflowProjectionIssue("issue-41", "ISSUE-41", domain.StatusDone, domain.PriorityLow)}
	}
	issueRepo := &boardRecordingIssueRepository{
		listResult: domain.IssueList{Items: ready, HasMore: true},
		listResultsByStatus: map[domain.Status]domain.IssueList{
			domain.StatusReady:  {Items: ready, HasMore: true},
			domain.StatusReview: {Items: reviewOf(), HasMore: true},
			domain.StatusDone:   {Items: doneOf(), HasMore: true},
		},
	}
	attemptRepo := &boardRecordingAttemptRepository{}
	reservationRepo := &boardRecordingReservationRepository{}
	reviewRepo := &boardRecordingReviewRepository{
		listResultsByStatus: map[domain.ReviewRequestStatus]ports.ListReviewRequestsResult{
			domain.ReviewRequestStatusBlocked: {HasMore: true},
		},
	}
	graphRepo := &boardRecordingGraphRepository{snapshot: domain.GraphSnapshot{}}
	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
	truncated, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}

	result, err := truncated.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}
	if !result.Workflow.Truncation.Ready {
		t.Fatal("Truncation.Ready = false, want true")
	}
	if !result.Workflow.Truncation.Unprojected {
		t.Fatal("Truncation.Unprojected = false, want true")
	}
	if !result.Workflow.Truncation.Verifying {
		t.Fatal("Truncation.Verifying = false, want true")
	}
	if !result.Workflow.Truncation.Done {
		t.Fatal("Truncation.Done = false, want true")
	}
	if !result.Workflow.Truncation.ReviewRequests {
		t.Fatal("Truncation.ReviewRequests = false, want true")
	}
	if !result.Workflow.Truncation.Any() {
		t.Fatal("Truncation.Any() = false, want true")
	}
}

// TestBoardServiceWorkflowNewestReviewDecisionWins pins the wrong-column fix:
// the newest review request decides the column, even when it carries a
// terminal status the projection does not otherwise use. An older
// changes_requested or blocked request must not leave a card in RC or
// DECISION REQUIRED after a later approval or cancellation.
func TestBoardServiceWorkflowNewestReviewDecisionWins(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	ready := []domain.IssueProjection{
		workflowProjectionIssue("issue-20", "ISSUE-20", domain.StatusReady, domain.PriorityMedium),
		workflowProjectionIssue("issue-21", "ISSUE-21", domain.StatusReady, domain.PriorityMedium),
	}
	rest := []domain.IssueProjection{
		workflowProjectionIssue("issue-50", "ISSUE-50", domain.StatusBlocked, domain.PriorityHigh),
	}
	older := func(id, issueID string, status domain.ReviewRequestStatus) domain.ReviewRequest {
		return domain.ReviewRequest{ID: id, IssueID: issueID, Status: status, TargetIssueVersion: 2, CreatedAt: now.Add(-2 * time.Hour)}
	}
	newer := func(id, issueID string, status domain.ReviewRequestStatus) domain.ReviewRequest {
		return domain.ReviewRequest{ID: id, IssueID: issueID, Status: status, TargetIssueVersion: 3, CreatedAt: now.Add(-time.Hour)}
	}

	issueRepo := &boardRecordingIssueRepository{
		listResult:          domain.IssueList{Items: rest},
		listResultsByStatus: map[domain.Status]domain.IssueList{domain.StatusReady: {Items: ready}},
	}
	attemptRepo := &boardRecordingAttemptRepository{}
	reservationRepo := &boardRecordingReservationRepository{}
	reviewRepo := &boardRecordingReviewRepository{
		listResultsByStatus: map[domain.ReviewRequestStatus]ports.ListReviewRequestsResult{
			domain.ReviewRequestStatusChangesRequested: {Items: []domain.ReviewRequest{
				older("review-20-old", "issue-20", domain.ReviewRequestStatusChangesRequested),
				older("review-21-old", "issue-21", domain.ReviewRequestStatusChangesRequested),
			}},
			domain.ReviewRequestStatusBlocked: {Items: []domain.ReviewRequest{
				older("review-50-old", "issue-50", domain.ReviewRequestStatusBlocked),
			}},
			domain.ReviewRequestStatusApproved: {Items: []domain.ReviewRequest{
				newer("review-20-new", "issue-20", domain.ReviewRequestStatusApproved),
				newer("review-50-new", "issue-50", domain.ReviewRequestStatusApproved),
			}},
			domain.ReviewRequestStatusCancelled: {Items: []domain.ReviewRequest{
				newer("review-21-new", "issue-21", domain.ReviewRequestStatusCancelled),
			}},
		},
	}
	graphRepo := &boardRecordingGraphRepository{snapshot: domain.GraphSnapshot{}}
	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
	service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	columns := map[string]domain.BoardWorkflowColumn{}
	reviewStatus := map[string]*domain.ReviewRequestStatus{}
	for _, card := range result.Workflow.Cards {
		columns[card.IssueDisplayID] = card.Column
		reviewStatus[card.IssueDisplayID] = card.ReviewStatus
	}
	if columns["ISSUE-20"] != domain.BoardWorkflowColumnReady {
		t.Fatalf("ISSUE-20 column = %q, want READY: the newest review approved, the older changes_requested is obsolete", columns["ISSUE-20"])
	}
	if columns["ISSUE-21"] != domain.BoardWorkflowColumnReady {
		t.Fatalf("ISSUE-21 column = %q, want READY: the newest review was cancelled", columns["ISSUE-21"])
	}
	if reviewStatus["ISSUE-20"] == nil || *reviewStatus["ISSUE-20"] != domain.ReviewRequestStatusApproved {
		t.Fatalf("ISSUE-20 review status = %v, want the newest approved request", reviewStatus["ISSUE-20"])
	}
	reasons := map[string]string{}
	for _, item := range result.Workflow.Unprojected {
		reasons[item.IssueDisplayID] = item.Reason
	}
	if reasons["ISSUE-50"] != domain.BoardWorkflowReasonExternallyBlocked {
		t.Fatalf("ISSUE-50 unprojected reason = %q, want externally_blocked after a later approval", reasons["ISSUE-50"])
	}

	// The selection is deterministic even when two requests share an instant.
	created := now.Add(-30 * time.Minute)
	for _, order := range [][]domain.ReviewRequest{
		{
			{ID: "review-b", IssueID: "issue-1", Status: domain.ReviewRequestStatusApproved, CreatedAt: created},
			{ID: "review-a", IssueID: "issue-1", Status: domain.ReviewRequestStatusChangesRequested, CreatedAt: created},
		},
		{
			{ID: "review-a", IssueID: "issue-1", Status: domain.ReviewRequestStatusChangesRequested, CreatedAt: created},
			{ID: "review-b", IssueID: "issue-1", Status: domain.ReviewRequestStatusApproved, CreatedAt: created},
		},
	} {
		signals := indexReviewSignals(order)
		if signals["issue-1"].latest.ID != "review-b" {
			t.Fatalf("same-instant tie-break chose %q, want the higher request ID review-b", signals["issue-1"].latest.ID)
		}
	}
}
