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
		workflowProjectionIssue("issue-20", "ISSUE-20", domain.StatusReady, domain.PriorityMedium), // changes_requested is card detail -> READY
		workflowProjectionIssue("issue-21", "ISSUE-21", domain.StatusReady, domain.PriorityMedium), // -> READY
		workflowProjectionIssue("issue-22", "ISSUE-22", domain.StatusReady, domain.PriorityLow),    // -> READY
	}
	rank := int64(4)
	ready[2].ReadyRank = &rank
	review := []domain.IssueProjection{
		workflowProjectionIssue("issue-30", "ISSUE-30", domain.StatusReview, domain.PriorityHigh),   // active review attempt -> IN PROGRESS
		workflowProjectionIssue("issue-31", "ISSUE-31", domain.StatusReview, domain.PriorityMedium), // -> IN PROGRESS
	}
	done := []domain.IssueProjection{
		workflowProjectionIssue("issue-40", "ISSUE-40", domain.StatusDone, domain.PriorityLow),
	}
	blocked := []domain.IssueProjection{
		workflowProjectionIssue("issue-50", "ISSUE-50", domain.StatusBlocked, domain.PriorityHigh), // blocked review -> BLOCKED
		workflowProjectionIssue("issue-60", "ISSUE-60", domain.StatusBlocked, domain.PriorityLow),  // external block -> BLOCKED
	}
	decision := "product decision required"
	external := "waiting on the vendor API"
	blocked[0].BlockedReason = &decision
	blocked[1].BlockedReason = &external
	rest := []domain.IssueProjection{
		workflowProjectionIssue("issue-70", "ISSUE-70", domain.StatusOpen, domain.PriorityLow), // not ready -> unprojected
	}

	issueRepo := &boardRecordingIssueRepository{
		countResult: []domain.EffectiveStatusCount{{EffectiveStatus: domain.EffectiveStatusReady, Count: 6}},
		listResult:  domain.IssueList{Items: rest},
		listResultsByStatus: map[domain.Status]domain.IssueList{
			domain.StatusReady:   {Items: ready},
			domain.StatusReview:  {Items: review},
			domain.StatusBlocked: {Items: blocked},
			domain.StatusDone:    {Items: done},
		},
		blockedResult: &domain.IssueList{Items: []domain.IssueProjection{blocked[0]}},
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
		domain.BoardWorkflowColumnReady:      {"ISSUE-20", "ISSUE-21", "ISSUE-22"},
		domain.BoardWorkflowColumnInProgress: {"ISSUE-10", "ISSUE-30", "ISSUE-31"},
		domain.BoardWorkflowColumnDone:       {"ISSUE-40"},
		domain.BoardWorkflowColumnBlocked:    {"ISSUE-50", "ISSUE-60"},
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
	if len(result.Workflow.Unprojected) != 1 {
		t.Fatalf("Unprojected = %#v, want only the open issue no column describes", result.Workflow.Unprojected)
	}
	reasons := map[string]string{}
	for _, item := range result.Workflow.Unprojected {
		reasons[item.IssueDisplayID] = item.Reason
	}
	if reasons["ISSUE-70"] != domain.BoardWorkflowReasonNotReady {
		t.Fatalf("unprojected reasons = %#v", reasons)
	}

	// AB-5: the four columns are the whole board. The retired process-phase
	// states must not reappear as columns.
	for _, column := range result.Workflow.Columns {
		switch column.Column {
		case domain.BoardWorkflowColumnReady, domain.BoardWorkflowColumnInProgress,
			domain.BoardWorkflowColumnDone, domain.BoardWorkflowColumnBlocked:
		default:
			t.Fatalf("unexpected workflow column %q", column.Column)
		}
	}
	if len(result.Workflow.Columns) != 4 {
		t.Fatalf("columns = %#v, want exactly the four task-level columns", result.Workflow.Columns)
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
// and instance key, and a review attempt is labelled as the verifier while
// staying in the same task-level column as developer work.
func TestBoardServiceWorkflowCardShowsExecutorRuntimeMetadata(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, _ := workflowBoardFixture(t, now)

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	var inProgress, verifying *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		if card.Column != domain.BoardWorkflowColumnInProgress {
			continue
		}
		switch card.IssueDisplayID {
		case "ISSUE-10":
			inProgress = &result.Workflow.Cards[index]
		case "ISSUE-30":
			verifying = &result.Workflow.Cards[index]
		}
	}
	if inProgress == nil || verifying == nil {
		t.Fatalf("missing the developer or the verifier IN PROGRESS card: %#v", result.Workflow.Cards)
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
	if verifying.Column != domain.BoardWorkflowColumnInProgress {
		t.Fatalf("verifier card column = %q, want IN PROGRESS", verifying.Column)
	}
	if verifying.AttemptKind != domain.AttemptKindReview {
		t.Fatalf("verifier attempt kind = %q, want %q", verifying.AttemptKind, domain.AttemptKindReview)
	}
	if verifying.ReviewStatus == nil || *verifying.ReviewStatus != domain.ReviewRequestStatusClaimed {
		t.Fatalf("under-review card review status = %v, want claimed", verifying.ReviewStatus)
	}

	// AB-5: the changes-requested round is card detail on a READY task, not an
	// RC column, and a blocked review is a BLOCKED task, not DECISION REQUIRED.
	var changesRequested, decision *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		switch card.IssueDisplayID {
		case "ISSUE-20":
			changesRequested = &result.Workflow.Cards[index]
		case "ISSUE-50":
			decision = &result.Workflow.Cards[index]
		}
	}
	if changesRequested == nil || changesRequested.Column != domain.BoardWorkflowColumnReady {
		t.Fatalf("changes-requested card = %#v, want READY with the round as detail", changesRequested)
	}
	if changesRequested.ReviewStatus == nil || *changesRequested.ReviewStatus != domain.ReviewRequestStatusChangesRequested {
		t.Fatalf("changes-requested review status = %v, want changes_requested", changesRequested.ReviewStatus)
	}
	if changesRequested.ChangesRequestedCount != 1 {
		t.Fatalf("changes_requested_count = %d, want 1", changesRequested.ChangesRequestedCount)
	}
	if changesRequested.AttemptID != "" {
		t.Fatalf("rework-ready card attempt = %q, want no active attempt", changesRequested.AttemptID)
	}
	if decision == nil || decision.Column != domain.BoardWorkflowColumnBlocked {
		t.Fatalf("blocked-review card = %#v, want BLOCKED", decision)
	}
	if decision.ReviewStatus == nil || *decision.ReviewStatus != domain.ReviewRequestStatusBlocked {
		t.Fatalf("blocked card review status = %v, want blocked detail", decision.ReviewStatus)
	}
	if decision.BlockedReason == nil || *decision.BlockedReason != "product decision required" {
		t.Fatalf("blocked card reason = %v, want the stored reason", decision.BlockedReason)
	}
	// The external block carries its own reason; the board does not classify it.
	var external *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		if card.IssueDisplayID == "ISSUE-60" {
			external = &result.Workflow.Cards[index]
		}
	}
	if external == nil || external.Column != domain.BoardWorkflowColumnBlocked ||
		external.BlockedReason == nil || *external.BlockedReason != "waiting on the vendor API" {
		t.Fatalf("external block card = %#v, want BLOCKED with its stored reason", external)
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
	blockedOf := func() []domain.IssueProjection {
		return []domain.IssueProjection{workflowProjectionIssue("issue-51", "ISSUE-51", domain.StatusBlocked, domain.PriorityHigh)}
	}
	doneOf := func() []domain.IssueProjection {
		return []domain.IssueProjection{workflowProjectionIssue("issue-41", "ISSUE-41", domain.StatusDone, domain.PriorityLow)}
	}
	issueRepo := &boardRecordingIssueRepository{
		listResult: domain.IssueList{Items: ready, HasMore: true},
		listResultsByStatus: map[domain.Status]domain.IssueList{
			domain.StatusReady:   {Items: ready, HasMore: true},
			domain.StatusReview:  {Items: reviewOf(), HasMore: true},
			domain.StatusBlocked: {Items: blockedOf(), HasMore: true},
			domain.StatusDone:    {Items: doneOf(), HasMore: true},
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
	if !result.Workflow.Truncation.Review {
		t.Fatal("Truncation.Review = false, want true")
	}
	if !result.Workflow.Truncation.Blocked {
		t.Fatal("Truncation.Blocked = false, want true")
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

// TestBoardServiceWorkflowNewestReviewDecisionWins pins the review-detail
// rule and the AB-5 boundary at once: the newest review request is what a card
// reports, even when it carries a terminal status, and no review outcome can
// change the card's task-level column.
func TestBoardServiceWorkflowNewestReviewDecisionWins(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	ready := []domain.IssueProjection{
		workflowProjectionIssue("issue-20", "ISSUE-20", domain.StatusReady, domain.PriorityMedium),
		workflowProjectionIssue("issue-21", "ISSUE-21", domain.StatusReady, domain.PriorityMedium),
	}
	rest := []domain.IssueProjection{
		workflowProjectionIssue("issue-80", "ISSUE-80", domain.StatusOpen, domain.PriorityLow),
	}
	blocked := []domain.IssueProjection{
		workflowProjectionIssue("issue-50", "ISSUE-50", domain.StatusBlocked, domain.PriorityHigh),
	}
	older := func(id, issueID string, status domain.ReviewRequestStatus) domain.ReviewRequest {
		return domain.ReviewRequest{ID: id, IssueID: issueID, Status: status, TargetIssueVersion: 2, CreatedAt: now.Add(-2 * time.Hour)}
	}
	newer := func(id, issueID string, status domain.ReviewRequestStatus) domain.ReviewRequest {
		return domain.ReviewRequest{ID: id, IssueID: issueID, Status: status, TargetIssueVersion: 3, CreatedAt: now.Add(-time.Hour)}
	}

	issueRepo := &boardRecordingIssueRepository{
		listResult: domain.IssueList{Items: rest},
		listResultsByStatus: map[domain.Status]domain.IssueList{
			domain.StatusReady:   {Items: ready},
			domain.StatusBlocked: {Items: blocked},
		},
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
		t.Fatalf("ISSUE-20 column = %q, want READY: stored ready is READY whatever the review history", columns["ISSUE-20"])
	}
	if columns["ISSUE-21"] != domain.BoardWorkflowColumnReady {
		t.Fatalf("ISSUE-21 column = %q, want READY: stored ready is READY whatever the review history", columns["ISSUE-21"])
	}
	if columns["ISSUE-50"] != domain.BoardWorkflowColumnBlocked {
		t.Fatalf("ISSUE-50 column = %q, want BLOCKED: a later approval is detail, not a column move", columns["ISSUE-50"])
	}
	if reviewStatus["ISSUE-20"] == nil || *reviewStatus["ISSUE-20"] != domain.ReviewRequestStatusApproved {
		t.Fatalf("ISSUE-20 review status = %v, want the newest approved request", reviewStatus["ISSUE-20"])
	}
	if reviewStatus["ISSUE-21"] == nil || *reviewStatus["ISSUE-21"] != domain.ReviewRequestStatusCancelled {
		t.Fatalf("ISSUE-21 review status = %v, want the newest cancelled request", reviewStatus["ISSUE-21"])
	}
	if reviewStatus["ISSUE-50"] == nil || *reviewStatus["ISSUE-50"] != domain.ReviewRequestStatusApproved {
		t.Fatalf("ISSUE-50 review status = %v, want the newest approved request despite the older blocked one", reviewStatus["ISSUE-50"])
	}
	reasons := map[string]string{}
	for _, item := range result.Workflow.Unprojected {
		reasons[item.IssueDisplayID] = item.Reason
	}
	if reasons["ISSUE-80"] != domain.BoardWorkflowReasonNotReady {
		t.Fatalf("ISSUE-80 unprojected reason = %q, want not_ready", reasons["ISSUE-80"])
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

// TestBoardServiceWorkflowReworkCardKeepsItsDelivery pins the AB-5 delivery
// rule: a stored-ready card sent back for changes is READY again, and it must
// keep the delivery references its earlier round produced (the retired RC card
// carried them). A READY card that has never been reviewed is still skipped,
// so the board does not read artifacts for work that cannot have any.
func TestBoardServiceWorkflowReworkCardKeepsItsDelivery(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, reader := workflowBoardFixture(t, now)
	title := "fix: first round"
	reader.byIssue["issue-20"] = []domain.Artifact{
		{ID: "artifact-20", IssueID: "issue-20", Type: domain.ArtifactTypeCommit, URI: "deadbee", Title: &title},
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}
	var rework *domain.BoardWorkflowCard
	for index, card := range result.Workflow.Cards {
		if card.IssueDisplayID == "ISSUE-20" {
			rework = &result.Workflow.Cards[index]
		}
	}
	if rework == nil || rework.Column != domain.BoardWorkflowColumnReady {
		t.Fatalf("rework card = %#v, want READY", rework)
	}
	if len(rework.Delivery) != 1 || rework.Delivery[0].URI != "deadbee" {
		t.Fatalf("rework READY card delivery = %#v, want the earlier round's commit", rework.Delivery)
	}
	asked := map[string]bool{}
	for _, issueID := range reader.called {
		asked[issueID] = true
	}
	if !asked["issue-20"] {
		t.Fatal("delivery reader was never asked for the rework card")
	}
	for _, neverReviewed := range []string{"issue-21", "issue-22"} {
		if asked[neverReviewed] {
			t.Fatalf("delivery reader was asked for untouched READY issue %q", neverReviewed)
		}
	}
}

// TestBoardServiceReadyQueueRefusesWhenTheAttemptReadWasCut keeps reordering
// honest: when the active-attempt read is cut, an attempt that would place a
// stored-ready issue in IN PROGRESS can be missing, so the plan could not be
// trusted and the queue refuses.
func TestBoardServiceReadyQueueRefusesWhenTheAttemptReadWasCut(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	build := func(t *testing.T, hasMore bool) *BoardService {
		t.Helper()
		ready := []domain.IssueProjection{workflowProjectionIssue("issue-10", "ISSUE-10", domain.StatusReady, domain.PriorityHigh)}
		issueRepo := &boardRecordingIssueRepository{
			listResultsByStatus: map[domain.Status]domain.IssueList{domain.StatusReady: {Items: ready}},
		}
		attemptRepo := &boardRecordingAttemptRepository{listResult: domain.ActiveAttemptList{HasMore: hasMore}}
		issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(
			t, issueRepo, attemptRepo, &boardRecordingReservationRepository{}, &boardRecordingReviewRepository{},
			&boardRecordingGraphRepository{snapshot: domain.GraphSnapshot{}}, now)
		service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
		if err != nil {
			t.Fatalf("NewBoardService() error = %v", err)
		}
		return service
	}

	snapshot, err := build(t, true).ReadyQueue(context.Background())
	if err != nil {
		t.Fatalf("ReadyQueue() error = %v", err)
	}
	if !snapshot.Truncated {
		t.Fatal("ReadyQueue().Truncated = false with a cut active-attempt read")
	}
	intact, err := build(t, false).ReadyQueue(context.Background())
	if err != nil {
		t.Fatalf("ReadyQueue() error = %v", err)
	}
	if intact.Truncated {
		t.Fatal("ReadyQueue().Truncated = true with complete reads")
	}
	if len(intact.Cards) != 1 || intact.Cards[0].IssueDisplayID != "ISSUE-10" {
		t.Fatalf("ReadyQueue() cards = %#v, want the stored-ready issue", intact.Cards)
	}
}
