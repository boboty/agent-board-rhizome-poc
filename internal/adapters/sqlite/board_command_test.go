package sqlite_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"rhizome-mcp/internal/application"
	"rhizome-mcp/internal/domain"
)

// stubReadyQueue is the READY column the command service plans over. By
// default it mirrors every stored-ready issue (the mechanics tests); a curated
// list models what the workflow projection actually displays.
type stubReadyQueue struct {
	issues  *application.IssueService
	cards   []domain.BoardWorkflowCard
	trunc   bool
	allRead bool
}

func (stub *stubReadyQueue) ReadyQueue(ctx context.Context) (application.ReadyQueueSnapshot, error) {
	if !stub.allRead {
		return application.ReadyQueueSnapshot{Cards: stub.cards, Truncated: stub.trunc}, nil
	}
	page, err := stub.issues.ListIssues(ctx, domain.ListIssuesInput{
		Statuses: []domain.Status{domain.StatusReady}, Limit: domain.MaxBoardCollectionLimit,
	})
	if err != nil {
		return application.ReadyQueueSnapshot{}, err
	}
	cards := make([]domain.BoardWorkflowCard, 0, len(page.Items))
	for _, item := range page.Items {
		cards = append(cards, domain.BoardWorkflowCard{
			Column: domain.BoardWorkflowColumnReady, IssueID: item.ID, IssueDisplayID: item.DisplayID,
			Title: item.Title, Type: item.Type, Priority: item.Priority, StoredStatus: item.Status,
			Version: item.Version, ReadyRank: item.ReadyRank,
		})
	}
	return application.ReadyQueueSnapshot{Cards: cards, Truncated: page.HasMore}, nil
}

func boardCommandService(t *testing.T) (*application.BoardCommandService, *application.IssueService) {
	t.Helper()
	issues, _, _ := openIssueService(t)
	commands, err := application.NewBoardCommandService(issues, &stubReadyQueue{issues: issues, allRead: true})
	if err != nil {
		t.Fatalf("NewBoardCommandService() error = %v", err)
	}
	return commands, issues
}

func readyQueueOrder(t *testing.T, issues *application.IssueService) []domain.IssueProjection {
	t.Helper()
	page, err := issues.ListIssues(context.Background(), domain.ListIssuesInput{
		Statuses: []domain.Status{domain.StatusReady}, Limit: domain.MaxBoardCollectionLimit,
	})
	if err != nil {
		t.Fatalf("ListIssues() error = %v", err)
	}
	items := append([]domain.IssueProjection(nil), page.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		return domain.CompareReadyQueue(
			domain.ReadyQueueEntry{DisplayID: items[i].DisplayID, ReadyRank: items[i].ReadyRank, Priority: items[i].Priority},
			domain.ReadyQueueEntry{DisplayID: items[j].DisplayID, ReadyRank: items[j].ReadyRank, Priority: items[j].Priority},
		) < 0
	})
	return items
}

// TestBoardCommandServiceCreatesEditsQueuesAndReorders covers the whole minimal
// write loop against real SQLite: create defaults to open, an edit persists and
// bumps the version, a stale version is a conflict that changes nothing, an
// open task is queued into READY, and a READY reorder persists and leaves every
// other status untouched.
func TestBoardCommandServiceCreatesEditsQueuesAndReorders(t *testing.T) {
	commands, issues := boardCommandService(t)
	ctx := context.Background()

	created, err := commands.CreateTask(ctx, application.CreateBoardTaskInput{
		Fields: application.BoardTaskFields{Title: "  Write something  "},
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if created.Issue.Status != domain.StatusOpen || created.Issue.Type != domain.TypeTask {
		t.Fatalf("created task = %#v, want an open task", created.Issue)
	}
	if created.Issue.Priority != domain.PriorityMedium {
		t.Fatalf("created priority = %q, want the medium default", created.Issue.Priority)
	}
	if created.Issue.Title != "Write something" {
		t.Fatalf("created title = %q, want it trimmed", created.Issue.Title)
	}

	description := "First description"
	criteria := "First criteria"
	edited, err := commands.UpdateTask(ctx, application.UpdateBoardTaskInput{
		IssueID: created.Issue.ID, ExpectedVersion: created.Issue.Version,
		Fields: application.BoardTaskFields{
			Title: "Edited title", Description: &description, AcceptanceCriteria: &criteria,
			Priority: domain.PriorityHigh,
		},
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if edited.Issue.Title != "Edited title" || edited.Issue.Priority != domain.PriorityHigh ||
		edited.Issue.Description == nil || *edited.Issue.Description != description ||
		edited.Issue.AcceptanceCriteria == nil || *edited.Issue.AcceptanceCriteria != criteria {
		t.Fatalf("edited task = %#v", edited.Issue)
	}
	if edited.Issue.Version != created.Issue.Version+1 {
		t.Fatalf("edited version = %d, want %d", edited.Issue.Version, created.Issue.Version+1)
	}

	// A stale edit must conflict and change nothing.
	if _, err := commands.UpdateTask(ctx, application.UpdateBoardTaskInput{
		IssueID: created.Issue.ID, ExpectedVersion: created.Issue.Version,
		Fields: application.BoardTaskFields{Title: "Stale overwrite", Priority: domain.PriorityLow},
	}); !errors.Is(err, &domain.Error{Code: domain.CodeVersionConflict}) {
		t.Fatalf("stale UpdateTask() error = %v, want VERSION_CONFLICT", err)
	}
	current, err := issues.GetIssue(ctx, created.Issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Title != "Edited title" || current.Version != edited.Issue.Version {
		t.Fatalf("stale edit mutated storage: %#v", current)
	}

	// Queue it: open -> ready through the ordinary update path.
	rank := int64(50)
	queued, err := commands.MoveTaskToReady(ctx, application.MoveBoardTaskToReadyInput{
		IssueID: created.Issue.ID, ExpectedVersion: current.Version, ReadyRank: &rank,
	})
	if err != nil {
		t.Fatalf("MoveTaskToReady() error = %v", err)
	}
	if queued.Issue.Status != domain.StatusReady || queued.Issue.ReadyRank == nil || *queued.Issue.ReadyRank != 50 {
		t.Fatalf("queued task = %#v, want ready with rank 50", queued.Issue)
	}

	// Two more READY tasks so the queue has an order to change.
	other := func(title string, rankValue *int64) domain.Issue {
		result, err := commands.CreateTask(ctx, application.CreateBoardTaskInput{
			Fields: application.BoardTaskFields{Title: title}, Status: domain.StatusReady, ReadyRank: rankValue,
		})
		if err != nil {
			t.Fatalf("CreateTask(%s) error = %v", title, err)
		}
		return result.Issue
	}
	rankTen, rankTwenty := int64(10), int64(20)
	first := other("First in queue", &rankTen)
	second := other("Second in queue", &rankTwenty)

	// A non-READY task whose ordering must never be touched by a reorder.
	unready, err := commands.CreateTask(ctx, application.CreateBoardTaskInput{
		Fields: application.BoardTaskFields{Title: "Not queued"},
	})
	if err != nil {
		t.Fatal(err)
	}
	untouched := unready.Issue

	order := readyQueueOrder(t, issues)
	if len(order) != 3 || order[0].ID != first.ID || order[1].ID != second.ID || order[2].ID != queued.Issue.ID {
		t.Fatalf("initial READY order = %v", []string{order[0].DisplayID, order[1].DisplayID, order[2].DisplayID})
	}

	// Move the last card up one position (past `second`).
	moved, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: queued.Issue.ID, ExpectedVersion: queued.Issue.Version, Direction: application.BoardReadyMoveUp,
	})
	if err != nil {
		t.Fatalf("MoveReadyTask(up) error = %v", err)
	}
	if moved.Updates == 0 {
		t.Fatal("MoveReadyTask(up) reported no updates")
	}
	order = readyQueueOrder(t, issues)
	if order[0].ID != first.ID || order[1].ID != queued.Issue.ID || order[2].ID != second.ID {
		t.Fatalf("READY order after move up = %v", []string{order[0].DisplayID, order[1].DisplayID, order[2].DisplayID})
	}
	for _, item := range order {
		if item.ReadyRank == nil {
			t.Fatalf("reorder left %s unranked", item.DisplayID)
		}
	}

	// The unqueued task kept its status, its (absent) rank, and its version.
	unreadyAfter, err := issues.GetIssue(ctx, untouched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unreadyAfter.Status != domain.StatusOpen || unreadyAfter.ReadyRank != nil || unreadyAfter.Version != untouched.Version {
		t.Fatalf("reorder touched a non-READY task: %#v", unreadyAfter)
	}

	// Reordering is bounded by the version the caller saw: `second` was
	// renumbered by the move above, so its pre-move version is stale.
	if _, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: second.ID, ExpectedVersion: second.Version, Direction: application.BoardReadyMoveUp,
	}); !errors.Is(err, &domain.Error{Code: domain.CodeVersionConflict}) {
		t.Fatalf("stale MoveReadyTask() error = %v, want VERSION_CONFLICT", err)
	}

	// An already-first card moving up is a successful no-op.
	fresh, err := issues.GetIssue(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: first.ID, ExpectedVersion: fresh.Version, Direction: application.BoardReadyMoveUp,
	})
	if err != nil {
		t.Fatalf("no-op MoveReadyTask(up) error = %v", err)
	}
	if noop.Updates != 0 {
		t.Fatalf("no-op MoveReadyTask(up) updates = %d, want 0", noop.Updates)
	}

	// Only READY tasks can be reordered through this operation.
	if _, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: unreadyAfter.ID, ExpectedVersion: unreadyAfter.Version, Direction: application.BoardReadyMoveUp,
	}); !errors.Is(err, &domain.Error{Code: domain.CodeInvalidArgument}) {
		t.Fatalf("MoveReadyTask(blocked) error = %v, want INVALID_ARGUMENT", err)
	}
}

// TestBoardCommandServiceRejectsUnsupportedStatuses keeps the board's write
// surface narrow: only open and ready can be created, and only up/down moves
// are accepted.
func TestBoardCommandServiceRejectsUnsupportedStatuses(t *testing.T) {
	commands, _ := boardCommandService(t)
	ctx := context.Background()

	if _, err := commands.CreateTask(ctx, application.CreateBoardTaskInput{
		Fields: application.BoardTaskFields{Title: "Done already"}, Status: domain.StatusDone,
	}); !errors.Is(err, &domain.Error{Code: domain.CodeInvalidArgument}) {
		t.Fatalf("CreateTask(done) error = %v, want INVALID_ARGUMENT", err)
	}
	if _, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ExpectedVersion: 1, Direction: "sideways",
	}); !errors.Is(err, &domain.Error{Code: domain.CodeInvalidArgument}) {
		t.Fatalf("MoveReadyTask(sideways) error = %v, want INVALID_ARGUMENT", err)
	}
}

// TestBoardCommandServiceQueueOnlyAcceptsOpenTasks keeps the board's queue
// action narrow: done -> ready is a legal issue transition, so without this
// guard a crafted request could reopen finished work through the queue button.
func TestBoardCommandServiceQueueOnlyAcceptsOpenTasks(t *testing.T) {
	commands, issues := boardCommandService(t)
	ctx := context.Background()

	// A stored done task (created directly through the issue service, as a
	// project with the completion gate satisfied would).
	finished, err := issues.CreateIssue(ctx, domain.CreateIssueInput{
		Type: domain.TypeTask, Title: "Already finished", Status: domain.StatusDone,
	})
	if err != nil {
		t.Fatalf("create done task: %v", err)
	}
	if _, err := commands.MoveTaskToReady(ctx, application.MoveBoardTaskToReadyInput{
		IssueID: finished.Issue.ID, ExpectedVersion: finished.Issue.Version,
	}); !errors.Is(err, &domain.Error{Code: domain.CodeInvalidArgument}) {
		t.Fatalf("queueing a done task error = %v, want INVALID_ARGUMENT", err)
	}
	reloaded, err := issues.GetIssue(ctx, finished.Issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != domain.StatusDone || reloaded.Version != finished.Issue.Version {
		t.Fatalf("rejected queue changed the task: %#v", reloaded)
	}
}

// TestBoardCommandServiceReordersOnlyVisibleReadyCards pins the worst AB-4
// defect: the READY column is a projection, so a stored-ready issue displayed
// as IN PROGRESS is not in the queue. A reorder must plan over
// the displayed cards only and must never rewrite the rank or version of a
// card the operator could not see.
func TestBoardCommandServiceReordersOnlyVisibleReadyCards(t *testing.T) {
	issues, _, _ := openIssueService(t)
	ctx := context.Background()

	rankOf := func(t *testing.T, id string) *int64 {
		t.Helper()
		issue, err := issues.GetIssue(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return issue.ReadyRank
	}
	create := func(title string, rank int64) domain.Issue {
		t.Helper()
		value := rank
		result, err := issues.CreateIssue(ctx, domain.CreateIssueInput{
			Type: domain.TypeTask, Title: title, Status: domain.StatusReady, ReadyRank: &value,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Issue
	}

	first := create("Visible first", 10)
	hidden := create("Stored ready but displayed elsewhere", 20)
	second := create("Visible second", 30)

	// The projection shows only the two visible cards; `hidden` is displayed as
	// IN PROGRESS (it is claimed), so it is not part of the READY queue.
	visible := func(t *testing.T) []domain.BoardWorkflowCard {
		t.Helper()
		cards := make([]domain.BoardWorkflowCard, 0, 2)
		for _, issue := range []domain.Issue{first, second} {
			loaded, err := issues.GetIssue(ctx, issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			cards = append(cards, domain.BoardWorkflowCard{
				Column: domain.BoardWorkflowColumnReady, IssueID: loaded.ID, IssueDisplayID: loaded.DisplayID,
				Title: loaded.Title, Type: loaded.Type, Priority: loaded.Priority,
				StoredStatus: loaded.Status, Version: loaded.Version, ReadyRank: loaded.ReadyRank,
			})
		}
		return cards
	}
	commands, err := application.NewBoardCommandService(issues, &stubReadyQueue{cards: visible(t)})
	if err != nil {
		t.Fatal(err)
	}

	loadedSecond, err := issues.GetIssue(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: second.ID, ExpectedVersion: loadedSecond.Version, Direction: application.BoardReadyMoveUp,
	})
	if err != nil {
		t.Fatalf("MoveReadyTask() error = %v", err)
	}
	if moved.Updates == 0 {
		t.Fatal("moving the visible second card up reported no updates")
	}
	hiddenAfter, err := issues.GetIssue(ctx, hidden.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hiddenAfter.ReadyRank == nil || *hiddenAfter.ReadyRank != 20 || hiddenAfter.Version != hidden.Version {
		t.Fatalf("an invisible READY card was rewritten: %#v (was %#v)", hiddenAfter, hidden)
	}
	// The two visible cards swapped, so the displayed order really changed.
	firstAfter, secondAfter := rankOf(t, first.ID), rankOf(t, second.ID)
	if firstAfter == nil || secondAfter == nil || *secondAfter >= *firstAfter {
		t.Fatalf("visible order did not change: first=%v second=%v", firstAfter, secondAfter)
	}
}

// TestBoardCommandServiceRefusesTruncatedReadyQueue keeps the reorder honest
// when the READY column itself was cut at the collection limit.
func TestBoardCommandServiceRefusesTruncatedReadyQueue(t *testing.T) {
	issues, _, _ := openIssueService(t)
	ctx := context.Background()
	value := int64(10)
	created, err := issues.CreateIssue(ctx, domain.CreateIssueInput{
		Type: domain.TypeTask, Title: "Queued", Status: domain.StatusReady, ReadyRank: &value,
	})
	if err != nil {
		t.Fatal(err)
	}
	commands, err := application.NewBoardCommandService(issues, &stubReadyQueue{
		trunc: true,
		cards: []domain.BoardWorkflowCard{{
			Column: domain.BoardWorkflowColumnReady, IssueID: created.Issue.ID, IssueDisplayID: created.Issue.DisplayID,
			StoredStatus: domain.StatusReady, Version: created.Issue.Version, ReadyRank: created.Issue.ReadyRank,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := commands.MoveReadyTask(ctx, application.MoveBoardReadyTaskInput{
		IssueID: created.Issue.ID, ExpectedVersion: created.Issue.Version, Direction: application.BoardReadyMoveUp,
	}); !errors.Is(err, &domain.Error{Code: domain.CodeInvalidArgument}) {
		t.Fatalf("truncated queue reorder error = %v, want INVALID_ARGUMENT", err)
	}
}
