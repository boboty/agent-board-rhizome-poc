package application

import (
	"context"

	"rhizome-mcp/internal/domain"
)

// boardDeliveryReferenceLimit bounds how many delivery references one card
// carries. A card is a summary, not a delivery log; the issue-detail page is
// where the full artifact list lives.
const boardDeliveryReferenceLimit = 3

// boardWorkflowDeliveryTypes are the artifact types the board treats as a
// delivery reference. A file, directory, or log artifact says nothing about
// what was delivered, so it is not shown on a card.
var boardWorkflowDeliveryTypes = map[domain.ArtifactType]bool{
	domain.ArtifactTypeCommit:      true,
	domain.ArtifactTypeBranch:      true,
	domain.ArtifactTypePullRequest: true,
}

// boardDeliveryReferenceReader reads one issue's bounded delivery artifacts
// (satisfied by *WorkContextService). The bool reports whether the underlying
// artifact read was itself truncated, so a card that cannot show every
// reference is reported rather than silently short. It is optional: when it is
// nil the board renders cards without delivery references instead of failing,
// because a delivery reference is "show it when it exists", never a
// requirement.
type boardDeliveryReferenceReader interface {
	IssueDeliveryReferences(context.Context, string) ([]domain.Artifact, bool, error)
}

// boardWorkflowSources is the bounded set of issues and review requests one
// board read projects onto the workflow columns. Every list is already bounded
// by the query that produced it.
type boardWorkflowSources struct {
	readyIssues   []domain.IssueProjection
	reviewIssues  []domain.IssueProjection
	doneIssues    []domain.IssueProjection
	restIssues    []domain.IssueProjection
	reviewByIssue map[string]reviewSignal
	truncation    domain.BoardWorkflowTruncation
}

// reviewSignal is one issue's review state as the board read saw it: the most
// recent review request among the read states, plus how many changes-requested
// rounds were read for that issue.
type reviewSignal struct {
	latest                 domain.ReviewRequest
	changesRequestedRounds int
}

func (service *BoardService) collectBoardWorkflowSources(ctx context.Context, openReviews []domain.ReviewRequest) (boardWorkflowSources, error) {
	sources := boardWorkflowSources{truncation: domain.BoardWorkflowTruncation{}}

	readyIssues, err := service.listIssuesByStatuses(ctx, []domain.Status{domain.StatusReady})
	if err != nil {
		return boardWorkflowSources{}, err
	}
	sources.readyIssues, sources.truncation.Ready = readyIssues.Items, readyIssues.HasMore

	reviewIssues, err := service.listIssuesByStatuses(ctx, []domain.Status{domain.StatusReview})
	if err != nil {
		return boardWorkflowSources{}, err
	}
	sources.reviewIssues, sources.truncation.Verifying = reviewIssues.Items, reviewIssues.HasMore

	doneIssues, err := service.listIssuesByStatuses(ctx, []domain.Status{domain.StatusDone})
	if err != nil {
		return boardWorkflowSources{}, err
	}
	sources.doneIssues, sources.truncation.Done = doneIssues.Items, doneIssues.HasMore

	// Open, blocked, and cancelled issues never reach a workflow column, but
	// the board reports them as unprojected rather than dropping them
	// silently. Archived issues are excluded here as they are everywhere else
	// on the board (the issue read filters archived_at IS NULL), so the
	// archived guard in the projection is a defensive branch for direct
	// callers rather than a state this read can produce.
	restIssues, err := service.listIssuesByStatuses(ctx, []domain.Status{domain.StatusOpen, domain.StatusBlocked, domain.StatusCancelled})
	if err != nil {
		return boardWorkflowSources{}, err
	}
	sources.restIssues, sources.truncation.Unprojected = restIssues.Items, restIssues.HasMore

	// Review requests carry the RC / DECISION REQUIRED / VERIFYING signal. The
	// open page is already loaded by GetBoard, so it is passed in rather than
	// re-read.
	requests, truncated, err := service.collectWorkflowReviewRequests(ctx, openReviews)
	if err != nil {
		return boardWorkflowSources{}, err
	}
	sources.truncation.ReviewRequests = truncated
	sources.reviewByIssue = indexReviewSignals(requests)
	return sources, nil
}

// listIssuesByStatuses reads one bounded page of issues for a stored-status
// filter. Stored status is used rather than effective status because the
// projection needs to distinguish "stored ready and unclaimed" from "stored
// ready and being worked", which effective status alone collapses.
func (service *BoardService) listIssuesByStatuses(ctx context.Context, statuses []domain.Status) (domain.IssueList, error) {
	return service.issueService.ListIssues(ctx, domain.ListIssuesInput{
		Statuses: statuses,
		Limit:    domain.MaxBoardCollectionLimit,
	})
}

// collectWorkflowReviewRequests reads every review state that can make a
// card's newest review signal meaningful. The open page is passed in because
// GetBoard already loaded it.
//
// Superseded requests are deliberately not read: a superseded request always
// has a successor created later that is read here, so it can never be the
// newest decision. Approved and cancelled are read even though they are
// terminal for their round, because an issue can be reopened (done -> ready is
// a legal transition) or have a request withdrawn, leaving an older
// changes_requested or blocked request in the database. Skipping the newer
// decision would make that older request look like the newest one and place
// the card in RC or DECISION REQUIRED after the last real decision was the
// opposite.
func (service *BoardService) collectWorkflowReviewRequests(ctx context.Context, openReviews []domain.ReviewRequest) ([]domain.ReviewRequest, bool, error) {
	requests := make([]domain.ReviewRequest, 0, len(openReviews)+6*domain.MaxBoardCollectionLimit)
	requests = append(requests, openReviews...)
	truncated := false
	for _, status := range []domain.ReviewRequestStatus{
		domain.ReviewRequestStatusClaimed,
		domain.ReviewRequestStatusChangesRequested,
		domain.ReviewRequestStatusBlocked,
		domain.ReviewRequestStatusApproved,
		domain.ReviewRequestStatusCancelled,
	} {
		filter := string(status)
		page, err := service.reviewService.ListReviewRequests(ctx, ListReviewRequestsInput{
			Status: &filter,
			Limit:  domain.MaxBoardCollectionLimit,
		})
		if err != nil {
			return nil, false, err
		}
		if page.HasMore {
			truncated = true
		}
		for _, item := range page.Items {
			requests = append(requests, item.Request)
		}
	}
	return requests, truncated, nil
}

// indexReviewSignals reduces a flat, unordered review-request set to one
// signal per issue: the newest request seen, plus the number of
// changes-requested rounds seen. "Newest" is by creation time with the request
// ID as a deterministic tie-break, so two requests created in the same
// instant still resolve the same way on every read.
func indexReviewSignals(requests []domain.ReviewRequest) map[string]reviewSignal {
	signals := make(map[string]reviewSignal, len(requests))
	for _, request := range requests {
		issueID := request.IssueID
		if issueID == "" {
			continue
		}
		signal := signals[issueID]
		if signal.latest.ID == "" || requestIsNewer(request, signal.latest) {
			signal.latest = request
		}
		if request.Status == domain.ReviewRequestStatusChangesRequested {
			signal.changesRequestedRounds++
		}
		signals[issueID] = signal
	}
	return signals
}

func requestIsNewer(candidate, current domain.ReviewRequest) bool {
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return candidate.ID > current.ID
}

// buildBoardWorkflow derives the Kanban projection. One card is produced per
// distinct issue, so a task can never occupy two columns at once.
func (service *BoardService) buildBoardWorkflow(ctx context.Context, sources boardWorkflowSources, activeAttempts []domain.ActiveAttemptSummary) (domain.BoardWorkflowProjection, error) {
	attemptByIssue := make(map[string]domain.ActiveAttemptSummary, len(activeAttempts))
	for _, attempt := range activeAttempts {
		if _, exists := attemptByIssue[attempt.IssueID]; !exists {
			attemptByIssue[attempt.IssueID] = attempt
		}
	}

	candidates := make([]domain.IssueProjection, 0,
		len(sources.readyIssues)+len(sources.reviewIssues)+len(sources.doneIssues)+len(sources.restIssues))
	seen := make(map[string]struct{})
	for _, group := range [][]domain.IssueProjection{sources.readyIssues, sources.reviewIssues, sources.doneIssues, sources.restIssues} {
		for _, issue := range group {
			if _, exists := seen[issue.ID]; exists {
				continue
			}
			seen[issue.ID] = struct{}{}
			candidates = append(candidates, issue)
		}
	}

	cards := make([]domain.BoardWorkflowCard, 0, len(candidates))
	unprojected := make([]domain.BoardWorkflowUnprojected, 0)
	for _, issue := range candidates {
		var attempt *domain.ActiveAttemptSummary
		if active, ok := attemptByIssue[issue.ID]; ok {
			attempt = &active
		}
		var latestReview *domain.ReviewRequest
		rounds := 0
		if signal, ok := sources.reviewByIssue[issue.ID]; ok {
			review := signal.latest
			latestReview = &review
			rounds = signal.changesRequestedRounds
		}
		column, reason := domain.DeriveBoardWorkflowPlacement(domain.BoardWorkflowPlacementInput{
			Issue:         issue.Issue,
			ActiveAttempt: attempt,
			LatestReview:  latestReview,
		})
		if column == "" {
			unprojected = append(unprojected, domain.BoardWorkflowUnprojected{
				IssueID: issue.ID, IssueDisplayID: issue.DisplayID, Title: issue.Title,
				StoredStatus: issue.Status, Version: issue.Version,
				Reason: reason, Detail: domain.BoardWorkflowUnprojectedDetail(reason),
			})
			continue
		}
		cards = append(cards, buildBoardWorkflowCard(issue, column, attempt, latestReview, rounds))
	}

	if reader := service.deliveryReferences; reader != nil {
		overflow, unavailable := attachDeliveryReferences(ctx, cards, reader)
		sources.truncation.DeliveryOverflow = overflow
		sources.truncation.DeliveryUnavailable = unavailable
	}

	return domain.NewBoardWorkflowProjection(cards, unprojected, sources.truncation), nil
}

func buildBoardWorkflowCard(issue domain.IssueProjection, column domain.BoardWorkflowColumn, attempt *domain.ActiveAttemptSummary, review *domain.ReviewRequest, rounds int) domain.BoardWorkflowCard {
	card := domain.BoardWorkflowCard{
		Column: column, IssueID: issue.ID, IssueDisplayID: issue.DisplayID, Title: issue.Title,
		Type: issue.Type, Priority: issue.Priority, StoredStatus: issue.Status,
		Version: issue.Version, IsClaimable: issue.IsClaimable,
	}
	if column == domain.BoardWorkflowColumnReady {
		card.ReadyRank = boardInt64Pointer(issue.ReadyRank)
	}
	if attempt != nil {
		started := attempt.StartedAt.UTC()
		expires := attempt.LeaseExpiresAt.UTC()
		card.AttemptID = attempt.AttemptID
		card.AttemptKind = attempt.Kind
		card.ExecutorLabel = copyOptionalString(attempt.SessionLabel)
		card.ExecutorInstanceKey = copyOptionalString(attempt.SessionInstanceKey)
		card.ExecutorClient = copyOptionalString(attempt.SessionClientName)
		card.ExecutorModel = copyOptionalString(attempt.SessionModel)
		card.ExecutorWorktree = copyOptionalString(attempt.SessionWorktree)
		card.AttemptStartedAt = &started
		card.LeaseExpiresAt = &expires
	}
	if review != nil {
		status := review.Status
		target := review.TargetIssueVersion
		requested := review.CreatedAt.UTC()
		card.ReviewRequestID = workflowStringPointer(review.ID)
		card.ReviewStatus = &status
		card.ReviewTargetVersion = &target
		card.ReviewRequestedAt = &requested
		if review.ResolvedAt != nil {
			resolved := review.ResolvedAt.UTC()
			card.ReviewResolvedAt = &resolved
		}
	}
	if rounds > 0 {
		card.ChangesRequestedCount = rounds
	}
	return card
}

// attachDeliveryReferences fills in delivery references for every card that
// can have one. READY cards have no attempt and no delivery yet, so they are
// skipped. It returns whether any card shows fewer references than its issue
// has (the per-card cap cut the list, or the artifact read was itself
// truncated) and whether any card's references could not be read.
//
// A read failure degrades the affected card rather than failing the whole
// projection: delivery references are supplementary, and a board that cannot
// show one commit is still a useful board. The failure is reported through the
// truncation flags so it is visible, not swallowed.
func attachDeliveryReferences(ctx context.Context, cards []domain.BoardWorkflowCard, reader boardDeliveryReferenceReader) (bool, bool) {
	overflow := false
	unavailable := false
	for index := range cards {
		if cards[index].Column == domain.BoardWorkflowColumnReady {
			continue
		}
		artifacts, truncated, err := reader.IssueDeliveryReferences(ctx, cards[index].IssueID)
		if err != nil {
			unavailable = true
			continue
		}
		if truncated {
			// The issue has more artifacts than the reader returned, so a
			// delivery reference may have been cut before the card cap ran.
			overflow = true
		}
		references := make([]domain.BoardDeliveryReference, 0, boardDeliveryReferenceLimit)
		for _, artifact := range artifacts {
			if !boardWorkflowDeliveryTypes[artifact.Type] {
				continue
			}
			if len(references) == boardDeliveryReferenceLimit {
				overflow = true
				break
			}
			references = append(references, domain.BoardDeliveryReference{
				Type: artifact.Type, URI: artifact.URI, Title: copyOptionalString(artifact.Title),
			})
		}
		cards[index].Delivery = references
	}
	return overflow, unavailable
}

func workflowStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func boardInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
