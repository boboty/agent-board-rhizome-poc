package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"crypto/rand"
	"rhizome-mcp/internal/clock"
	"rhizome-mcp/internal/domain"
	"rhizome-mcp/internal/ids"
	"rhizome-mcp/internal/ports"
)

func TestNewBoardServiceRejectsNilDependencies(t *testing.T) {
	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependencies(t, time.Date(2026, 8, 7, 13, 14, 15, 0, time.UTC))

	tests := []struct {
		name        string
		issue       *IssueService
		attempt     *AttemptService
		reservation *ReservationService
		review      *ReviewService
		graph       *GraphService
		source      clock.Clock
		wantCode    string
	}{
		{name: "nil issue service", issue: nil, attempt: attemptService, reservation: reservationService, review: reviewService, graph: graphService, source: source, wantCode: domain.CodeInvalidArgument},
		{name: "nil attempt service", issue: issueService, attempt: nil, reservation: reservationService, review: reviewService, graph: graphService, source: source, wantCode: domain.CodeInvalidArgument},
		{name: "nil reservation service", issue: issueService, attempt: attemptService, reservation: nil, review: reviewService, graph: graphService, source: source, wantCode: domain.CodeInvalidArgument},
		{name: "nil review service", issue: issueService, attempt: attemptService, reservation: reservationService, review: nil, graph: graphService, source: source, wantCode: domain.CodeInvalidArgument},
		{name: "nil graph service", issue: issueService, attempt: attemptService, reservation: reservationService, review: reviewService, graph: nil, source: source, wantCode: domain.CodeInvalidArgument},
		{name: "nil clock", issue: issueService, attempt: attemptService, reservation: reservationService, review: reviewService, graph: graphService, source: nil, wantCode: domain.CodeInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBoardService(tt.issue, tt.attempt, tt.reservation, tt.review, tt.graph, &stubGateSummaryService{}, nil, tt.source)
			if !errors.Is(err, &domain.Error{Code: tt.wantCode}) {
				t.Fatalf("NewBoardService() error = %v, want %q", err, tt.wantCode)
			}
		})
	}
}

func TestBoardServiceGetBoardAggregatesBoundedCollectionsAndGraph(t *testing.T) {
	now := time.Date(2026, 8, 7, 13, 14, 15, 0, time.FixedZone("test", 2*60*60))
	issueRepo := &boardRecordingIssueRepository{
		countResult: []domain.EffectiveStatusCount{{EffectiveStatus: domain.EffectiveStatusOpen, Count: 2}, {EffectiveStatus: domain.EffectiveStatusInProgress, Count: 1}},
		listResult:  domain.IssueList{Items: []domain.IssueProjection{{Issue: domain.Issue{ID: "issue-1", DisplayID: "ISSUE-1"}, EffectiveStatus: domain.EffectiveStatusBlocked, IsBlocked: true}}},
	}
	attemptRepo := &boardRecordingAttemptRepository{listResult: domain.ActiveAttemptList{Items: []domain.ActiveAttemptSummary{{AttemptID: "attempt-1", IssueID: "issue-1", IssueDisplayID: "ISSUE-1", IssueTitle: "Work", Kind: domain.AttemptKindWork}}, HasMore: false}}
	reservationRepo := &boardRecordingReservationRepository{listResult: domain.ReservationList{Items: []domain.Reservation{{ID: "reservation-1", IssueID: "issue-1", AttemptID: "attempt-1", Kind: domain.ResourceKindFile, DisplayValue: "a.go", Status: domain.ReservationStatusActive}}}}
	reviewRepo := &boardRecordingReviewRepository{listResult: ports.ListReviewRequestsResult{Items: []domain.ReviewRequest{{ID: "review-1", IssueID: "issue-1", Status: domain.ReviewRequestStatusOpen}}}}
	graphRepo := &boardRecordingGraphRepository{snapshot: domain.GraphSnapshot{RootIssueID: boardStringPointer("issue-1"), Nodes: []domain.IssueProjection{{Issue: domain.Issue{ID: "issue-1", DisplayID: "ISSUE-1"}}}}}

	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
	service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	if !result.GeneratedAt.Equal(now.UTC()) || result.GeneratedAt.Location() != time.UTC {
		t.Fatalf("GeneratedAt = %v, want %v", result.GeneratedAt, now.UTC())
	}
	if !reflect.DeepEqual(result.StatusCounts, []domain.EffectiveStatusCount{{EffectiveStatus: domain.EffectiveStatusOpen, Count: 2}, {EffectiveStatus: domain.EffectiveStatusInProgress, Count: 1}}) {
		t.Fatalf("StatusCounts = %#v", result.StatusCounts)
	}
	if !reflect.DeepEqual(result.BlockedIssues, []domain.IssueProjection{{Issue: domain.Issue{ID: "issue-1", DisplayID: "ISSUE-1"}, EffectiveStatus: domain.EffectiveStatusBlocked, IsBlocked: true}}) {
		t.Fatalf("BlockedIssues = %#v", result.BlockedIssues)
	}
	if !reflect.DeepEqual(result.ActiveAttempts, []domain.ActiveAttemptSummary{{AttemptID: "attempt-1", IssueID: "issue-1", IssueDisplayID: "ISSUE-1", IssueTitle: "Work", Kind: domain.AttemptKindWork}}) {
		t.Fatalf("ActiveAttempts = %#v", result.ActiveAttempts)
	}
	if !reflect.DeepEqual(result.ActiveReservations, []domain.Reservation{{ID: "reservation-1", IssueID: "issue-1", AttemptID: "attempt-1", Kind: domain.ResourceKindFile, DisplayValue: "a.go", Status: domain.ReservationStatusActive}}) {
		t.Fatalf("ActiveReservations = %#v", result.ActiveReservations)
	}
	if !reflect.DeepEqual(result.ReviewRequests, []domain.ReviewRequest{{ID: "review-1", IssueID: "issue-1", Status: domain.ReviewRequestStatusOpen}}) {
		t.Fatalf("ReviewRequests = %#v", result.ReviewRequests)
	}
	if result.PlanningGraph.RootIssueID == nil || *result.PlanningGraph.RootIssueID != "issue-1" {
		t.Fatalf("PlanningGraph.RootIssueID = %#v", result.PlanningGraph.RootIssueID)
	}

	if !issueRepo.countCommand.Now.Equal(now.UTC()) || issueRepo.countCommand.Now.Location() != time.UTC {
		t.Fatalf("issue count clock = %v", issueRepo.countCommand.Now)
	}
	blockedCommand, ok := boardBlockedListCommand(issueRepo)
	if !ok || blockedCommand.Input.IsBlocked == nil || !*blockedCommand.Input.IsBlocked {
		t.Fatalf("blocked filter = %#v", blockedCommand.Input.IsBlocked)
	}
	if blockedCommand.Input.Limit != domain.MaxBoardCollectionLimit {
		t.Fatalf("blocked limit = %d", blockedCommand.Input.Limit)
	}
	if attemptRepo.listCommand.Limit != domain.MaxBoardCollectionLimit {
		t.Fatalf("active attempt limit = %d", attemptRepo.listCommand.Limit)
	}
	if reservationRepo.listCommand.Input.Active == nil || !*reservationRepo.listCommand.Input.Active {
		t.Fatalf("active reservations filter = %#v", reservationRepo.listCommand.Input.Active)
	}
	if reservationRepo.listCommand.Input.Limit != domain.MaxBoardCollectionLimit {
		t.Fatalf("active reservation limit = %d", reservationRepo.listCommand.Input.Limit)
	}
	openReviewQuery, ok := boardReviewListQuery(reviewRepo, domain.ReviewRequestStatusOpen)
	if !ok || openReviewQuery.Status == nil || *openReviewQuery.Status != domain.ReviewRequestStatusOpen {
		t.Fatalf("review status query = %#v", openReviewQuery.Status)
	}
	if openReviewQuery.Limit != domain.MaxBoardCollectionLimit {
		t.Fatalf("review limit = %d", openReviewQuery.Limit)
	}
	if graphRepo.command.RootIdentifier != nil {
		t.Fatalf("graph root identifier = %#v", graphRepo.command.RootIdentifier)
	}
	if !graphRepo.command.Now.Equal(now.UTC()) || graphRepo.command.Now.Location() != time.UTC {
		t.Fatalf("graph clock = %v", graphRepo.command.Now)
	}
}

func TestBoardServiceShortCircuitsAtEachDependencyBoundary(t *testing.T) {
	now := time.Date(2026, 8, 7, 13, 14, 15, 0, time.UTC)
	tests := []struct {
		name                  string
		configure             func(*boardRecordingIssueRepository, *boardRecordingAttemptRepository, *boardRecordingReservationRepository, *boardRecordingReviewRepository, *boardRecordingGraphRepository)
		wantCountCalled       bool
		wantListCalled        bool
		wantAttemptCalled     bool
		wantReservationCalled bool
		wantReviewCalled      bool
		wantGraphCalled       bool
	}{
		{
			name: "count issues",
			configure: func(issueRepo *boardRecordingIssueRepository, _ *boardRecordingAttemptRepository, _ *boardRecordingReservationRepository, _ *boardRecordingReviewRepository, _ *boardRecordingGraphRepository) {
				issueRepo.countErr = errors.New("count failed")
			},
			wantCountCalled: true,
		},
		{
			name: "list issues",
			configure: func(issueRepo *boardRecordingIssueRepository, _ *boardRecordingAttemptRepository, _ *boardRecordingReservationRepository, _ *boardRecordingReviewRepository, _ *boardRecordingGraphRepository) {
				issueRepo.listErr = errors.New("list failed")
			},
			wantCountCalled: true,
			wantListCalled:  true,
		},
		{
			name: "active attempts",
			configure: func(_ *boardRecordingIssueRepository, attemptRepo *boardRecordingAttemptRepository, _ *boardRecordingReservationRepository, _ *boardRecordingReviewRepository, _ *boardRecordingGraphRepository) {
				attemptRepo.listErr = errors.New("attempts failed")
			},
			wantCountCalled:   true,
			wantListCalled:    true,
			wantAttemptCalled: true,
		},
		{
			name: "active reservations",
			configure: func(_ *boardRecordingIssueRepository, _ *boardRecordingAttemptRepository, reservationRepo *boardRecordingReservationRepository, _ *boardRecordingReviewRepository, _ *boardRecordingGraphRepository) {
				reservationRepo.listErr = errors.New("reservations failed")
			},
			wantCountCalled:       true,
			wantListCalled:        true,
			wantAttemptCalled:     true,
			wantReservationCalled: true,
		},
		{
			name: "review requests",
			configure: func(_ *boardRecordingIssueRepository, _ *boardRecordingAttemptRepository, _ *boardRecordingReservationRepository, reviewRepo *boardRecordingReviewRepository, _ *boardRecordingGraphRepository) {
				reviewRepo.listErr = errors.New("reviews failed")
			},
			wantCountCalled:       true,
			wantListCalled:        true,
			wantAttemptCalled:     true,
			wantReservationCalled: true,
			wantReviewCalled:      true,
		},
		{
			name: "planning graph",
			configure: func(_ *boardRecordingIssueRepository, _ *boardRecordingAttemptRepository, _ *boardRecordingReservationRepository, _ *boardRecordingReviewRepository, graphRepo *boardRecordingGraphRepository) {
				graphRepo.err = errors.New("graph failed")
			},
			wantCountCalled:       true,
			wantListCalled:        true,
			wantAttemptCalled:     true,
			wantReservationCalled: true,
			wantReviewCalled:      true,
			wantGraphCalled:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issueRepo := &boardRecordingIssueRepository{}
			attemptRepo := &boardRecordingAttemptRepository{}
			reservationRepo := &boardRecordingReservationRepository{}
			reviewRepo := &boardRecordingReviewRepository{}
			graphRepo := &boardRecordingGraphRepository{}
			tt.configure(issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo)

			issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
			service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
			if err != nil {
				t.Fatalf("NewBoardService() error = %v", err)
			}

			_, err = service.GetBoard(context.Background())
			if err == nil {
				t.Fatal("GetBoard() error = nil, want failure")
			}

			if issueRepo.countCalled != tt.wantCountCalled || issueRepo.listCalled != tt.wantListCalled || attemptRepo.listCalled != tt.wantAttemptCalled || reservationRepo.listCalled != tt.wantReservationCalled || reviewRepo.listCalled != tt.wantReviewCalled || graphRepo.called != tt.wantGraphCalled {
				t.Fatalf("calls = count:%t list:%t attempts:%t reservations:%t reviews:%t graph:%t", issueRepo.countCalled, issueRepo.listCalled, attemptRepo.listCalled, reservationRepo.listCalled, reviewRepo.listCalled, graphRepo.called)
			}
		})
	}
}

func newBoardServiceDependencies(t *testing.T, now time.Time) (*IssueService, *AttemptService, *ReservationService, *ReviewService, *GraphService, clock.Clock) {
	t.Helper()
	return newBoardServiceDependenciesWithRepos(t, &boardRecordingIssueRepository{}, &boardRecordingAttemptRepository{}, &boardRecordingReservationRepository{}, &boardRecordingReviewRepository{}, &boardRecordingGraphRepository{}, now)
}

func newBoardServiceDependenciesWithRepos(t *testing.T, issueRepo ports.IssueRepository, attemptRepo ports.AttemptRepository, reservationRepo ports.ReservationRepository, reviewRepo ports.ReviewRepository, graphRepo ports.GraphRepository, now time.Time) (*IssueService, *AttemptService, *ReservationService, *ReviewService, *GraphService, clock.Clock) {
	t.Helper()
	issueService, err := NewIssueService(issueRepo, clock.NewFakeClock(now), testIDGenerator{id: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if err != nil {
		t.Fatalf("NewIssueService() error = %v", err)
	}
	attemptService, err := NewAttemptService(attemptRepo, clock.NewFakeClock(now), testIDGenerator{id: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if err != nil {
		t.Fatalf("NewAttemptService() error = %v", err)
	}
	reservationService, err := NewReservationService(reservationRepo)
	if err != nil {
		t.Fatalf("NewReservationService() error = %v", err)
	}
	fakeClock := clock.NewFakeClock(now)
	generator, _ := ids.NewGenerator(fakeClock, rand.Reader)
	reviewService, err := NewReviewService(reviewRepo, issueRepo, fakeClock, generator)
	if err != nil {
		t.Fatalf("NewReviewService() error = %v", err)
	}
	graphService, err := NewGraphService(graphRepo, clock.NewFakeClock(now))
	if err != nil {
		t.Fatalf("NewGraphService() error = %v", err)
	}
	return issueService, attemptService, reservationService, reviewService, graphService, clock.NewFakeClock(now)
}

type testIDGenerator struct {
	id  string
	err error
}

func (generator testIDGenerator) New() (string, error) { return generator.id, generator.err }

type boardRecordingIssueRepository struct {
	countCommand ports.CountIssuesByEffectiveStatusCommand
	listCommand  ports.ListIssuesCommand
	// listCommands records every ListIssues call in order. The board issues
	// more than one issue read (the blocked page plus one per workflow
	// projection status group), so a test asserting on a specific read must
	// search the recorded calls rather than trust the last one.
	listCommands []ports.ListIssuesCommand
	countResult  []domain.EffectiveStatusCount
	listResult   domain.IssueList
	// listResultsByStatus overrides listResult for a single-status read.
	listResultsByStatus map[domain.Status]domain.IssueList
	// blockedResult overrides listResult for the IsBlocked read, which the
	// board issues alongside its stored-status reads.
	blockedResult *domain.IssueList
	countErr      error
	listErr       error
	countCalled   bool
	listCalled    bool
}

func (repository *boardRecordingIssueRepository) CreateIssue(context.Context, ports.CreateIssueCommand) (domain.Issue, error) {
	return domain.Issue{}, nil
}

func (repository *boardRecordingIssueRepository) LookupCreateIssue(context.Context, string, []byte) (domain.Issue, bool, error) {
	return domain.Issue{}, false, nil
}

func (repository *boardRecordingIssueRepository) UpdateIssue(context.Context, ports.UpdateIssueCommand) (ports.UpdateIssueResult, error) {
	return ports.UpdateIssueResult{}, nil
}

func (repository *boardRecordingIssueRepository) LookupUpdateIssue(context.Context, string, []byte) (ports.UpdateIssueResult, bool, error) {
	return ports.UpdateIssueResult{}, false, nil
}

func (repository *boardRecordingIssueRepository) ArchiveIssue(context.Context, ports.ArchiveIssueCommand) (ports.ArchiveIssueResult, error) {
	return ports.ArchiveIssueResult{}, nil
}

func (repository *boardRecordingIssueRepository) LookupArchiveIssue(context.Context, string, []byte) (ports.ArchiveIssueResult, bool, error) {
	return ports.ArchiveIssueResult{}, false, nil
}

func (repository *boardRecordingIssueRepository) UnarchiveIssue(context.Context, ports.UnarchiveIssueCommand) (ports.UnarchiveIssueResult, error) {
	return ports.UnarchiveIssueResult{}, nil
}

func (repository *boardRecordingIssueRepository) LookupUnarchiveIssue(context.Context, string, []byte) (ports.UnarchiveIssueResult, bool, error) {
	return ports.UnarchiveIssueResult{}, false, nil
}

func (repository *boardRecordingIssueRepository) GetIssue(context.Context, domain.IssueIdentifier) (domain.Issue, error) {
	return domain.Issue{}, nil
}

func (repository *boardRecordingIssueRepository) GetIssueProjection(context.Context, ports.GetIssueProjectionCommand) (domain.IssueProjection, error) {
	return domain.IssueProjection{}, nil
}

func (repository *boardRecordingIssueRepository) ListLabels(context.Context, ports.ListLabelsCommand) (domain.LabelList, error) {
	return domain.LabelList{}, nil
}

func (repository *boardRecordingIssueRepository) ListIssues(_ context.Context, command ports.ListIssuesCommand) (domain.IssueList, error) {
	repository.listCalled = true
	repository.listCommand = command
	repository.listCommands = append(repository.listCommands, command)
	if command.Input.IsBlocked != nil && repository.blockedResult != nil {
		return *repository.blockedResult, repository.listErr
	}
	if len(command.Input.Statuses) == 1 {
		if result, ok := repository.listResultsByStatus[command.Input.Statuses[0]]; ok {
			return result, repository.listErr
		}
	}
	return repository.listResult, repository.listErr
}

// boardBlockedListCommand returns the recorded blocked-issues read, which is
// the one ListIssues call made with an IsBlocked filter.
func boardBlockedListCommand(repository *boardRecordingIssueRepository) (ports.ListIssuesCommand, bool) {
	for _, command := range repository.listCommands {
		if command.Input.IsBlocked != nil {
			return command, true
		}
	}
	return ports.ListIssuesCommand{}, false
}

func (repository *boardRecordingIssueRepository) CountIssuesByEffectiveStatus(_ context.Context, command ports.CountIssuesByEffectiveStatusCommand) ([]domain.EffectiveStatusCount, error) {
	repository.countCalled = true
	repository.countCommand = command
	return repository.countResult, repository.countErr
}

type boardRecordingAttemptRepository struct {
	listCommand ports.ListActiveAttemptsCommand
	listResult  domain.ActiveAttemptList
	listErr     error
	listCalled  bool
}

func (repository *boardRecordingAttemptRepository) ClaimIssue(context.Context, ports.ClaimIssueCommand) (ports.ClaimIssueResult, error) {
	return ports.ClaimIssueResult{}, nil
}

func (repository *boardRecordingAttemptRepository) RenewAttempt(context.Context, ports.RenewAttemptCommand) (ports.RenewAttemptResult, error) {
	return ports.RenewAttemptResult{}, nil
}

func (repository *boardRecordingAttemptRepository) SaveAttemptNote(context.Context, ports.SaveAttemptNoteCommand) (ports.SaveAttemptNoteResult, error) {
	return ports.SaveAttemptNoteResult{}, nil
}

func (repository *boardRecordingAttemptRepository) LookupSaveAttemptNote(context.Context, string, []byte) (ports.SaveAttemptNoteResult, bool, error) {
	return ports.SaveAttemptNoteResult{}, false, nil
}

func (repository *boardRecordingAttemptRepository) LookupFinishedAttempt(context.Context, string, []byte) (ports.FinishAttemptResult, bool, error) {
	return ports.FinishAttemptResult{}, false, nil
}

func (repository *boardRecordingAttemptRepository) FinishAttempt(context.Context, ports.FinishAttemptCommand) (ports.FinishAttemptResult, error) {
	return ports.FinishAttemptResult{}, nil
}

func (repository *boardRecordingAttemptRepository) ForceReleaseAttempt(context.Context, ports.ForceReleaseAttemptCommand) (ports.ForceReleaseAttemptResult, error) {
	return ports.ForceReleaseAttemptResult{}, nil
}

func (repository *boardRecordingAttemptRepository) ExpireAttempts(context.Context, ports.ExpireAttemptsCommand) (ports.ExpireAttemptsResult, error) {
	return ports.ExpireAttemptsResult{}, nil
}

func (repository *boardRecordingAttemptRepository) ListActiveAttempts(_ context.Context, command ports.ListActiveAttemptsCommand) (domain.ActiveAttemptList, error) {
	repository.listCalled = true
	repository.listCommand = command
	return repository.listResult, repository.listErr
}

func (repository *boardRecordingAttemptRepository) SubmitGateEvidence(context.Context, ports.SubmitGateEvidenceCommand) (ports.SubmitGateEvidenceResult, error) {
	return ports.SubmitGateEvidenceResult{}, nil
}

func (repository *boardRecordingAttemptRepository) LookupSubmitGateEvidence(context.Context, string, []byte) (ports.SubmitGateEvidenceResult, bool, error) {
	return ports.SubmitGateEvidenceResult{}, false, nil
}

func (repository *boardRecordingAttemptRepository) ListAttemptEvidence(context.Context, ports.ListAttemptEvidenceCommand) ([]domain.AttemptEvidence, error) {
	return nil, nil
}

func (repository *boardRecordingAttemptRepository) ReserveResources(context.Context, ports.ReserveResourcesCommand) (ports.ReserveResourcesResult, error) {
	return ports.ReserveResourcesResult{}, nil
}

func (repository *boardRecordingAttemptRepository) LookupReserveResources(context.Context, string, []byte) (ports.ReserveResourcesResult, bool, error) {
	return ports.ReserveResourcesResult{}, false, nil
}

func (repository *boardRecordingAttemptRepository) ReleaseResources(context.Context, ports.ReleaseResourcesCommand) (ports.ReleaseResourcesResult, error) {
	return ports.ReleaseResourcesResult{}, nil
}

func (repository *boardRecordingAttemptRepository) LookupReleaseResources(context.Context, string, []byte) (ports.ReleaseResourcesResult, bool, error) {
	return ports.ReleaseResourcesResult{}, false, nil
}

type boardRecordingReservationRepository struct {
	listCommand ports.ListReservationsCommand
	listResult  domain.ReservationList
	listErr     error
	listCalled  bool
}

func (repository *boardRecordingReservationRepository) AcquireReservations(context.Context, ports.AcquireReservationsCommand) ([]domain.Reservation, error) {
	return nil, nil
}

func (repository *boardRecordingReservationRepository) LookupAcquireReservations(context.Context, string, []byte) ([]domain.Reservation, bool, error) {
	return nil, false, nil
}

func (repository *boardRecordingReservationRepository) ReleaseReservation(context.Context, ports.ReleaseReservationCommand) (domain.Reservation, error) {
	return domain.Reservation{}, nil
}

func (repository *boardRecordingReservationRepository) ListActiveReservations(context.Context, ports.ListActiveReservationsQuery) ([]domain.Reservation, error) {
	return nil, nil
}

func (repository *boardRecordingReservationRepository) ListReservationHistory(context.Context, ports.ListReservationHistoryQuery) ([]domain.Reservation, error) {
	return nil, nil
}

func (repository *boardRecordingReservationRepository) ListReservations(_ context.Context, command ports.ListReservationsCommand) (domain.ReservationList, error) {
	repository.listCalled = true
	repository.listCommand = command
	return repository.listResult, repository.listErr
}

func (repository *boardRecordingReservationRepository) GetReservation(context.Context, string) (domain.Reservation, error) {
	return domain.Reservation{}, nil
}

type boardRecordingReviewRepository struct {
	listQuery ports.ListReviewRequestsQuery
	// listQueries records every ListReviewRequests call in order: the board
	// reads open (for its existing collection), then claimed,
	// changes_requested, and blocked for the workflow projection.
	listQueries []ports.ListReviewRequestsQuery
	listResult  ports.ListReviewRequestsResult
	// listResultsByStatus overrides listResult for one status filter.
	listResultsByStatus map[domain.ReviewRequestStatus]ports.ListReviewRequestsResult
	listErr             error
	listCalled          bool
}

func (repository *boardRecordingReviewRepository) CreateReviewRequest(context.Context, ports.CreateReviewRequestCommand) (ports.CreateReviewRequestResult, error) {
	return ports.CreateReviewRequestResult{}, nil
}

func (repository *boardRecordingReviewRepository) GetReviewRequest(context.Context, string) (ports.GetReviewRequestResult, error) {
	return ports.GetReviewRequestResult{}, nil
}

func (repository *boardRecordingReviewRepository) ListReviewRequests(_ context.Context, query ports.ListReviewRequestsQuery) (ports.ListReviewRequestsResult, error) {
	repository.listCalled = true
	repository.listQuery = query
	repository.listQueries = append(repository.listQueries, query)
	if query.Status != nil {
		if result, ok := repository.listResultsByStatus[*query.Status]; ok {
			return result, repository.listErr
		}
	}
	return repository.listResult, repository.listErr
}

// boardReviewListQuery returns the recorded read for one review status.
func boardReviewListQuery(repository *boardRecordingReviewRepository, status domain.ReviewRequestStatus) (ports.ListReviewRequestsQuery, bool) {
	for _, query := range repository.listQueries {
		if query.Status != nil && *query.Status == status {
			return query, true
		}
	}
	return ports.ListReviewRequestsQuery{}, false
}

func (repository *boardRecordingReviewRepository) CancelReviewRequest(context.Context, ports.ReviewMutationCommand) (ports.ReviewMutationResult, error) {
	return ports.ReviewMutationResult{}, nil
}

func (repository *boardRecordingReviewRepository) ClaimReviewRequest(context.Context, ports.ReviewMutationCommand) (ports.ReviewMutationResult, error) {
	return ports.ReviewMutationResult{}, nil
}

func (repository *boardRecordingReviewRepository) ResolveReviewRequest(context.Context, ports.ResolveReviewRequestCommand) (ports.ResolveReviewRequestResult, error) {
	return ports.ResolveReviewRequestResult{}, nil
}

func (repository *boardRecordingReviewRepository) ReplaceReviewRequest(context.Context, ports.ReplaceReviewRequestCommand) (ports.ReplaceReviewRequestResult, error) {
	return ports.ReplaceReviewRequestResult{}, nil
}

func (repository *boardRecordingReviewRepository) LookupReplaceReviewRequest(context.Context, string, []byte) (ports.ReplaceReviewRequestResult, bool, error) {
	return ports.ReplaceReviewRequestResult{}, false, nil
}

type boardRecordingGraphRepository struct {
	command  ports.LoadGraphCommand
	snapshot domain.GraphSnapshot
	err      error
	called   bool
}

func (repository *boardRecordingGraphRepository) LoadGraph(_ context.Context, command ports.LoadGraphCommand) (domain.GraphSnapshot, error) {
	repository.called = true
	repository.command = command
	return repository.snapshot, repository.err
}

func boardStringPointer(value string) *string {
	copy := value
	return &copy
}

// TestBoardServiceBuildsAttemptGateProgress pins ISSUE-175 AC2's board rows:
// one gate-progress row per active attempt, in attempt order, carrying the
// summary the gate service reports for that attempt's issue.
func TestBoardServiceBuildsAttemptGateProgress(t *testing.T) {
	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	attemptRepo := &boardRecordingAttemptRepository{listResult: domain.ActiveAttemptList{Items: []domain.ActiveAttemptSummary{
		{AttemptID: "attempt-1", IssueID: "issue-1", IssueDisplayID: "ISSUE-1", Kind: domain.AttemptKindWork},
		{AttemptID: "attempt-2", IssueID: "issue-2", IssueDisplayID: "ISSUE-2", Kind: domain.AttemptKindWork},
	}, HasMore: false}}
	gateService := &stubGateSummaryService{summary: domain.WorkContextGateSummary{
		Point:            domain.EnforcementPointCompleteWorkToDone,
		RequirementCount: 2,
		SatisfiedCount:   1,
		Unmet:            []domain.WorkContextUnmetRequirement{{PolicyID: "policy-1", RequirementKey: "impl", Reason: "attempt evidence \"impl\" missing"}},
	}}

	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, &boardRecordingIssueRepository{}, attemptRepo, &boardRecordingReservationRepository{}, &boardRecordingReviewRepository{}, &boardRecordingGraphRepository{}, now)
	service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, gateService, nil, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}
	if !reflect.DeepEqual(gateService.calls, []string{"issue-1", "issue-2"}) {
		t.Fatalf("gate service calls = %#v, want one per active attempt's issue", gateService.calls)
	}
	if len(result.AttemptGates) != 2 {
		t.Fatalf("AttemptGates = %#v, want 2 rows", result.AttemptGates)
	}
	first := result.AttemptGates[0]
	if first.AttemptID != "attempt-1" || first.IssueID != "issue-1" || first.IssueDisplayID != "ISSUE-1" {
		t.Fatalf("first row identity = %#v", first)
	}
	if !reflect.DeepEqual(first.Gates, gateService.summary) {
		t.Fatalf("first row summary = %#v, want the gate service's summary", first.Gates)
	}
}

// TestBoardServiceFailsWhenGateSummaryFails: the board is one consistent
// aggregate; a gate read failure fails the board like any other collaborator.
func TestBoardServiceFailsWhenGateSummaryFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	attemptRepo := &boardRecordingAttemptRepository{listResult: domain.ActiveAttemptList{Items: []domain.ActiveAttemptSummary{{AttemptID: "attempt-1", IssueID: "issue-1", Kind: domain.AttemptKindWork}}, HasMore: false}}
	gateService := &stubGateSummaryService{err: domain.NewError(domain.CodeStorageUnavailable, "gate read failed", false)}

	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, &boardRecordingIssueRepository{}, attemptRepo, &boardRecordingReservationRepository{}, &boardRecordingReviewRepository{}, &boardRecordingGraphRepository{}, now)
	service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, gateService, nil, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}

	if _, err := service.GetBoard(context.Background()); !errors.Is(err, &domain.Error{Code: domain.CodeStorageUnavailable}) {
		t.Fatalf("GetBoard() error = %v, want the gate service failure", err)
	}
}

// TestBoardServiceGetBoardReportsPerCollectionTruncation verifies that
// GetBoard correctly reports truncation for each bounded board collection.
// This tests that HasMore flags from repositories propagate to the result's
// Truncation field, covering the four collections independently and in
// combination.
func TestBoardServiceGetBoardReportsPerCollectionTruncation(t *testing.T) {
	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name                   string
		blockedHasMore         bool
		attemptsHasMore        bool
		reservationsHasMore    bool
		reviewsHasMore         bool
		wantBlockedIssues      bool
		wantActiveAttempts     bool
		wantActiveReservations bool
		wantReviewRequests     bool
		wantTruncationAny      bool
	}{
		{
			name:                   "no truncation",
			blockedHasMore:         false,
			attemptsHasMore:        false,
			reservationsHasMore:    false,
			reviewsHasMore:         false,
			wantBlockedIssues:      false,
			wantActiveAttempts:     false,
			wantActiveReservations: false,
			wantReviewRequests:     false,
			wantTruncationAny:      false,
		},
		{
			name:                   "blocked issues truncated",
			blockedHasMore:         true,
			attemptsHasMore:        false,
			reservationsHasMore:    false,
			reviewsHasMore:         false,
			wantBlockedIssues:      true,
			wantActiveAttempts:     false,
			wantActiveReservations: false,
			wantReviewRequests:     false,
			wantTruncationAny:      true,
		},
		{
			name:                   "active attempts truncated",
			blockedHasMore:         false,
			attemptsHasMore:        true,
			reservationsHasMore:    false,
			reviewsHasMore:         false,
			wantBlockedIssues:      false,
			wantActiveAttempts:     true,
			wantActiveReservations: false,
			wantReviewRequests:     false,
			wantTruncationAny:      true,
		},
		{
			name:                   "active reservations truncated",
			blockedHasMore:         false,
			attemptsHasMore:        false,
			reservationsHasMore:    true,
			reviewsHasMore:         false,
			wantBlockedIssues:      false,
			wantActiveAttempts:     false,
			wantActiveReservations: true,
			wantReviewRequests:     false,
			wantTruncationAny:      true,
		},
		{
			name:                   "review requests truncated",
			blockedHasMore:         false,
			attemptsHasMore:        false,
			reservationsHasMore:    false,
			reviewsHasMore:         true,
			wantBlockedIssues:      false,
			wantActiveAttempts:     false,
			wantActiveReservations: false,
			wantReviewRequests:     true,
			wantTruncationAny:      true,
		},
		{
			name:                   "all truncated",
			blockedHasMore:         true,
			attemptsHasMore:        true,
			reservationsHasMore:    true,
			reviewsHasMore:         true,
			wantBlockedIssues:      true,
			wantActiveAttempts:     true,
			wantActiveReservations: true,
			wantReviewRequests:     true,
			wantTruncationAny:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issueRepo := &boardRecordingIssueRepository{
				listResult: domain.IssueList{Items: []domain.IssueProjection{}, HasMore: tt.blockedHasMore},
			}
			attemptRepo := &boardRecordingAttemptRepository{
				listResult: domain.ActiveAttemptList{Items: []domain.ActiveAttemptSummary{}, HasMore: tt.attemptsHasMore},
			}
			reservationRepo := &boardRecordingReservationRepository{
				listResult: domain.ReservationList{Items: []domain.Reservation{}, HasMore: tt.reservationsHasMore},
			}
			reviewRepo := &boardRecordingReviewRepository{
				listResult: ports.ListReviewRequestsResult{Items: []domain.ReviewRequest{}, HasMore: tt.reviewsHasMore},
			}
			graphRepo := &boardRecordingGraphRepository{}

			issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
			service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
			if err != nil {
				t.Fatalf("NewBoardService() error = %v", err)
			}

			result, err := service.GetBoard(context.Background())
			if err != nil {
				t.Fatalf("GetBoard() error = %v", err)
			}

			if result.Truncation.BlockedIssues != tt.wantBlockedIssues {
				t.Fatalf("Truncation.BlockedIssues = %v, want %v", result.Truncation.BlockedIssues, tt.wantBlockedIssues)
			}
			if result.Truncation.ActiveAttempts != tt.wantActiveAttempts {
				t.Fatalf("Truncation.ActiveAttempts = %v, want %v", result.Truncation.ActiveAttempts, tt.wantActiveAttempts)
			}
			if result.Truncation.ActiveReservations != tt.wantActiveReservations {
				t.Fatalf("Truncation.ActiveReservations = %v, want %v", result.Truncation.ActiveReservations, tt.wantActiveReservations)
			}
			if result.Truncation.ReviewRequests != tt.wantReviewRequests {
				t.Fatalf("Truncation.ReviewRequests = %v, want %v", result.Truncation.ReviewRequests, tt.wantReviewRequests)
			}
			if result.Truncation.Any() != tt.wantTruncationAny {
				t.Fatalf("Truncation.Any() = %v, want %v", result.Truncation.Any(), tt.wantTruncationAny)
			}
		})
	}
}

// TestBoardServiceReservationTruncationIsPreFilter verifies that the
// ActiveReservations truncation flag (D2 guarantee) comes from the
// pre-filter page's HasMore, not the post-filter reservation count.
// This proves that truncation detection happens before the attempt-filter,
// so a reader can trust "more exist" even when the filter removes rows.
func TestBoardServiceReservationTruncationIsPreFilter(t *testing.T) {
	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)

	// Create two reservations: one owned by an active attempt, one orphaned.
	issueRepo := &boardRecordingIssueRepository{}
	attemptRepo := &boardRecordingAttemptRepository{
		listResult: domain.ActiveAttemptList{
			Items: []domain.ActiveAttemptSummary{
				{AttemptID: "active-attempt", IssueID: "issue-1", IssueDisplayID: "ISSUE-1", Kind: domain.AttemptKindWork},
			},
			HasMore: false,
		},
	}
	reservationRepo := &boardRecordingReservationRepository{
		listResult: domain.ReservationList{
			Items: []domain.Reservation{
				{ID: "reservation-1", IssueID: "issue-1", AttemptID: "active-attempt", Kind: domain.ResourceKindFile, DisplayValue: "a.go", Status: domain.ReservationStatusActive},
				{ID: "reservation-2", IssueID: "issue-2", AttemptID: "orphaned-attempt", Kind: domain.ResourceKindFile, DisplayValue: "b.go", Status: domain.ReservationStatusActive},
			},
			HasMore: true, // Pre-filter page has more (indicates truncation)
		},
	}
	reviewRepo := &boardRecordingReviewRepository{
		listResult: ports.ListReviewRequestsResult{Items: []domain.ReviewRequest{}, HasMore: false},
	}
	graphRepo := &boardRecordingGraphRepository{}

	issueService, attemptService, reservationService, reviewService, graphService, source := newBoardServiceDependenciesWithRepos(t, issueRepo, attemptRepo, reservationRepo, reviewRepo, graphRepo, now)
	service, err := NewBoardService(issueService, attemptService, reservationService, reviewService, graphService, &stubGateSummaryService{}, nil, source)
	if err != nil {
		t.Fatalf("NewBoardService() error = %v", err)
	}

	result, err := service.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard() error = %v", err)
	}

	// The truncation flag must be true because the pre-filter page had HasMore=true
	if !result.Truncation.ActiveReservations {
		t.Fatalf("Truncation.ActiveReservations = %v, want true (from pre-filter HasMore)", result.Truncation.ActiveReservations)
	}

	// After filtering, only one reservation should remain (the one owned by the active attempt)
	if len(result.ActiveReservations) != 1 {
		t.Fatalf("len(ActiveReservations) = %d, want 1 (post-filter)", len(result.ActiveReservations))
	}
	if result.ActiveReservations[0].ID != "reservation-1" {
		t.Fatalf("ActiveReservations[0].ID = %q, want reservation-1", result.ActiveReservations[0].ID)
	}

	// D2 guarantee verified: truncation flag survives the filter and doesn't depend on post-filter length
}
