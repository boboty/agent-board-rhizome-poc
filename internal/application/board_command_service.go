package application

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"rhizome-mcp/internal/domain"
)

// boardReadyRankStep is the gap the board leaves between consecutive READY
// ranks when it renumbers the queue. V0.1 deliberately uses a fixed step and a
// full deterministic renumber instead of a sparse-ordering algorithm: the
// READY column is bounded by MaxBoardCollectionLimit, so the operation stays
// small, and the resulting order is exactly the order the operator asked for.
const boardReadyRankStep int64 = 10

// BoardTaskFields is the editable, human-facing task shape the served board
// exposes. It is intentionally narrower than domain.CreateIssueInput: the
// board's minimal write loop only creates and edits tasks, and only the fields
// a human edits on a card.
type BoardTaskFields struct {
	Title              string
	Description        *string
	AcceptanceCriteria *string
	Priority           domain.Priority
}

// CreateBoardTaskInput creates one task from the board. Status is optional and
// defaults to open; only open and ready are accepted, because those are the
// two states this write loop can intentionally produce. ReadyRank is optional
// and only meaningful when Status is ready.
type CreateBoardTaskInput struct {
	Fields    BoardTaskFields
	Status    domain.Status
	ReadyRank *int64
}

// CreateBoardTaskResult is the created task's persisted projection.
type CreateBoardTaskResult struct {
	Issue domain.Issue
}

// UpdateBoardTaskInput is a validated optimistic edit of the board-editable
// task fields. Every field in Fields is applied; description and acceptance
// criteria with a nil value clear the stored text.
type UpdateBoardTaskInput struct {
	IssueID         string
	ExpectedVersion int64
	Fields          BoardTaskFields
}

// UpdateBoardTaskResult is the persisted task after an edit or status move.
type UpdateBoardTaskResult struct {
	Issue domain.Issue
}

// MoveBoardTaskToReadyInput queues an existing task for work through the
// ordinary issue update path: stored status becomes ready, and the optional
// ReadyRank sets its explicit queue position.
type MoveBoardTaskToReadyInput struct {
	IssueID         string
	ExpectedVersion int64
	ReadyRank       *int64
}

// BoardReadyMoveDirection is the direction one READY card is moved relative to
// its neighbour in the displayed READY column.
type BoardReadyMoveDirection string

const (
	// BoardReadyMoveUp moves a card before the card currently above it.
	BoardReadyMoveUp BoardReadyMoveDirection = "up"
	// BoardReadyMoveDown moves a card after the card currently below it.
	BoardReadyMoveDown BoardReadyMoveDirection = "down"
)

// MoveBoardReadyTaskInput reorders one READY card. ExpectedVersion is the
// version the caller last saw, so a concurrent edit is reported rather than
// overwritten.
type MoveBoardReadyTaskInput struct {
	IssueID         string
	ExpectedVersion int64
	Direction       BoardReadyMoveDirection
}

// MoveBoardReadyTaskResult reports the moved task and how many issues were
// rewritten. Updates is zero when the card was already at the edge of its
// column, which is a successful no-op rather than an error.
type MoveBoardReadyTaskResult struct {
	Issue   domain.Issue
	Updates int
}

// boardReadyQueueReader returns the READY column as the board displays it
// (satisfied by *BoardService). Reordering must plan from this projection
// rather than from stored status: a stored-ready issue with an active attempt
// is shown as IN PROGRESS, and the operator cannot reorder a card they cannot
// see.
type boardReadyQueueReader interface {
	ReadyQueue(context.Context) (ReadyQueueSnapshot, error)
}

// BoardCommandService is the application entry point for the served board's
// minimal human write loop. It composes the existing IssueService and adds no
// storage of its own: create, edit, and queueing are the ordinary issue
// create/update commands, and reordering only rewrites the ready_rank column
// of issues that are already displayed in READY.
type BoardCommandService struct {
	issues     *IssueService
	readyQueue boardReadyQueueReader
}

// NewBoardCommandService composes the board write use case from the issue
// service it reuses and the board's READY-column read.
func NewBoardCommandService(issues *IssueService, readyQueue boardReadyQueueReader) (*BoardCommandService, error) {
	if issues == nil {
		return nil, domain.NewError(domain.CodeInvalidArgument, "board command service requires the issue service", false)
	}
	if readyQueue == nil {
		return nil, domain.NewError(domain.CodeInvalidArgument, "board command service requires the READY queue reader", false)
	}
	return &BoardCommandService{issues: issues, readyQueue: readyQueue}, nil
}

// CreateTask creates one task. An empty Status defaults to open, matching the
// board's "new work starts unqueued" rule.
func (service *BoardCommandService) CreateTask(ctx context.Context, input CreateBoardTaskInput) (CreateBoardTaskResult, error) {
	status := input.Status
	if status == "" {
		status = domain.StatusOpen
	}
	if status != domain.StatusOpen && status != domain.StatusReady {
		return CreateBoardTaskResult{}, unsupportedBoardWrite("status", string(status), "open or ready")
	}
	result, err := service.issues.CreateIssue(ctx, domain.CreateIssueInput{
		Type:               domain.TypeTask,
		Title:              strings.TrimSpace(input.Fields.Title),
		Description:        copyApplicationString(input.Fields.Description),
		AcceptanceCriteria: copyApplicationString(input.Fields.AcceptanceCriteria),
		Status:             status,
		Priority:           input.Fields.Priority,
		ReadyRank:          copyApplicationInt64(input.ReadyRank),
	})
	if err != nil {
		return CreateBoardTaskResult{}, err
	}
	return CreateBoardTaskResult{Issue: result.Issue}, nil
}

// UpdateTask applies a board edit through the existing optimistic update path.
func (service *BoardCommandService) UpdateTask(ctx context.Context, input UpdateBoardTaskInput) (UpdateBoardTaskResult, error) {
	result, err := service.issues.UpdateIssue(ctx, domain.UpdateIssueInput{
		IssueID:         input.IssueID,
		ExpectedVersion: input.ExpectedVersion,
		Changes: domain.IssuePatch{
			Title:              domain.OptionalValue[string]{Set: true, Value: strings.TrimSpace(input.Fields.Title)},
			Description:        domain.OptionalString{Set: true, Value: copyApplicationString(input.Fields.Description)},
			AcceptanceCriteria: domain.OptionalString{Set: true, Value: copyApplicationString(input.Fields.AcceptanceCriteria)},
			Priority:           domain.OptionalValue[domain.Priority]{Set: true, Value: input.Fields.Priority},
		},
	})
	if err != nil {
		return UpdateBoardTaskResult{}, err
	}
	return UpdateBoardTaskResult{Issue: result.Issue}, nil
}

// MoveTaskToReady queues a task for work. It is the ordinary status patch the
// board exposes as one button, with an optional explicit queue position.
func (service *BoardCommandService) MoveTaskToReady(ctx context.Context, input MoveBoardTaskToReadyInput) (UpdateBoardTaskResult, error) {
	// The action is "queue unstarted work", not "force this issue into ready".
	// done -> ready is a legal issue transition, so without this guard a crafted
	// request could reopen finished work through the queue button.
	current, err := service.issues.GetIssue(ctx, input.IssueID)
	if err != nil {
		return UpdateBoardTaskResult{}, err
	}
	if current.Status != domain.StatusOpen {
		return UpdateBoardTaskResult{}, domain.NewError(domain.CodeInvalidArgument,
			"only an open task can be queued into READY", false,
			domain.Detail{Field: "status", Code: "NOT_OPEN"})
	}
	changes := domain.IssuePatch{
		Status: domain.OptionalValue[domain.Status]{Set: true, Value: domain.StatusReady},
	}
	if input.ReadyRank != nil {
		changes.ReadyRank = domain.OptionalInt64{Set: true, Value: copyApplicationInt64(input.ReadyRank)}
	}
	result, err := service.issues.UpdateIssue(ctx, domain.UpdateIssueInput{
		IssueID:         input.IssueID,
		ExpectedVersion: input.ExpectedVersion,
		Changes:         changes,
	})
	if err != nil {
		return UpdateBoardTaskResult{}, err
	}
	return UpdateBoardTaskResult{Issue: result.Issue}, nil
}

// MoveReadyTask moves one READY card one position up or down and persists the
// resulting queue order as explicit ready_rank values.
//
// The plan is built from the card set and the comparator the board's READY
// column uses (BoardService.ReadyQueue + domain.CompareReadyQueue), so what the
// operator saw is what is reordered: a stored-ready issue displayed as IN
// PROGRESS is not part of the queue. Only those cards' ready_rank
// changes, so reordering can never alter another status's ordering.
//
// Every write carries the version read in this call, and the moved card must
// match the version the caller submitted, so a concurrent edit is reported as
// a version conflict instead of being overwritten. A conflict raised by a
// neighbour after earlier writes have already landed leaves the queue in a
// consistent (if partly renumbered) order; the caller refreshes and retries.
func (service *BoardCommandService) MoveReadyTask(ctx context.Context, input MoveBoardReadyTaskInput) (MoveBoardReadyTaskResult, error) {
	direction := input.Direction
	if direction != BoardReadyMoveUp && direction != BoardReadyMoveDown {
		return MoveBoardReadyTaskResult{}, unsupportedBoardWrite("direction", string(direction), "up or down")
	}
	if input.ExpectedVersion < 1 {
		return MoveBoardReadyTaskResult{}, domain.NewError(domain.CodeInvalidArgument, "expected_version is required", false,
			domain.Detail{Field: "expected_version", Code: "REQUIRED"})
	}
	// The board addresses issues by display ID, so resolve the identifier the
	// same way every other issue command does instead of assuming an internal
	// ULID.
	identifier, err := domain.ParseIssueIdentifier(input.IssueID)
	if err != nil {
		return MoveBoardReadyTaskResult{}, err
	}
	matchesIssue := func(card domain.BoardWorkflowCard) bool {
		if identifier.Kind == domain.IssueIdentifierInternalID {
			return card.IssueID == identifier.Value
		}
		return card.IssueDisplayID == "ISSUE-"+strconv.FormatInt(identifier.SequenceNo, 10)
	}

	snapshot, err := service.readyQueue.ReadyQueue(ctx)
	if err != nil {
		return MoveBoardReadyTaskResult{}, err
	}
	if snapshot.Truncated {
		// Renumbering a prefix of a longer queue would move the unread cards
		// relative to it, so the board refuses rather than reorder something it
		// could not see.
		return MoveBoardReadyTaskResult{}, domain.NewError(domain.CodeInvalidArgument,
			"READY queue is truncated; reorder is unavailable until it fits the board limit", false,
			domain.Detail{Field: "ready", Code: "TRUNCATED"})
	}
	// The snapshot already arrives in display order, but re-sorting with the
	// one shared comparator keeps the plan independent of read order.
	sorted := append([]domain.BoardWorkflowCard(nil), snapshot.Cards...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return domain.CompareReadyQueue(
			domain.ReadyQueueEntry{DisplayID: sorted[i].IssueDisplayID, ReadyRank: sorted[i].ReadyRank, Priority: sorted[i].Priority},
			domain.ReadyQueueEntry{DisplayID: sorted[j].IssueDisplayID, ReadyRank: sorted[j].ReadyRank, Priority: sorted[j].Priority},
		) < 0
	})

	type readyEntry struct {
		card    domain.BoardWorkflowCard
		current *int64
	}
	entries := make([]readyEntry, 0, len(sorted))
	index := -1
	for _, card := range sorted {
		entries = append(entries, readyEntry{card: card, current: card.ReadyRank})
		if matchesIssue(card) {
			index = len(entries) - 1
		}
	}
	if index < 0 {
		return MoveBoardReadyTaskResult{}, domain.NewError(domain.CodeInvalidArgument,
			"issue is not in the READY queue", false,
			domain.Detail{Field: "issue_id", Code: "NOT_READY"})
	}
	resolvedID := entries[index].card.IssueID
	if entries[index].card.Version != input.ExpectedVersion {
		return MoveBoardReadyTaskResult{}, domain.NewError(domain.CodeVersionConflict, "issue version conflict", true)
	}
	target := index - 1
	if direction == BoardReadyMoveDown {
		target = index + 1
	}
	if target < 0 || target >= len(entries) {
		// Already first or last: a successful no-op, so the UI does not have
		// to disable the button to stay correct.
		return MoveBoardReadyTaskResult{Issue: issueFromCard(entries[index].card)}, nil
	}
	entries[index], entries[target] = entries[target], entries[index]

	// Renumber the whole bounded READY column in the new order and write only
	// the issues whose rank actually changed.
	result := MoveBoardReadyTaskResult{Issue: issueFromCard(entries[target].card)}
	for position := range entries {
		next := int64(position+1) * boardReadyRankStep
		entry := entries[position]
		if entry.current != nil && *entry.current == next {
			continue
		}
		updated, err := service.issues.UpdateIssue(ctx, domain.UpdateIssueInput{
			IssueID:         entry.card.IssueID,
			ExpectedVersion: entry.card.Version,
			Changes: domain.IssuePatch{
				ReadyRank: domain.OptionalInt64{Set: true, Value: copyApplicationInt64(&next)},
			},
		})
		if err != nil {
			return MoveBoardReadyTaskResult{}, err
		}
		result.Updates++
		if entry.card.IssueID == resolvedID {
			result.Issue = updated.Issue
		}
	}
	return result, nil
}

// issueFromCard is the minimal issue projection a no-op reorder reports back.
func issueFromCard(card domain.BoardWorkflowCard) domain.Issue {
	return domain.Issue{
		ID: card.IssueID, DisplayID: card.IssueDisplayID, Type: card.Type, Title: card.Title,
		Status: card.StoredStatus, Priority: card.Priority, ReadyRank: card.ReadyRank, Version: card.Version,
	}
}

func unsupportedBoardWrite(field, value, allowed string) error {
	message := "unsupported " + field
	if strings.TrimSpace(value) != "" {
		message += " " + value
	}
	message += "; the board write API accepts " + allowed
	return domain.NewError(domain.CodeInvalidArgument, message, false,
		domain.Detail{Field: field, Code: "UNSUPPORTED", Message: allowed})
}

func copyApplicationInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
