package domain_test

import (
	"errors"
	"strings"
	"testing"

	"rhizome-mcp/internal/domain"
)

func TestCreateIssueInputValidateReadyRank(t *testing.T) {
	tests := []struct {
		name string
		rank *int64
		want *int64
	}{
		{name: "absent"},
		{name: "zero", rank: int64Pointer(0), want: int64Pointer(0)},
		{name: "maximum", rank: int64Pointer(domain.MaxReadyRank), want: int64Pointer(domain.MaxReadyRank)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, err := (domain.CreateIssueInput{
				Type: domain.TypeTask, Title: "queued", Status: domain.StatusReady, ReadyRank: test.rank,
			}).Validate()
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.want == nil {
				if input.ReadyRank != nil {
					t.Fatalf("ReadyRank = %v, want nil", *input.ReadyRank)
				}
				return
			}
			if input.ReadyRank == nil || *input.ReadyRank != *test.want {
				t.Fatalf("ReadyRank = %v, want %d", input.ReadyRank, *test.want)
			}
			// The normalized input must own its copy: mutating the caller's
			// value after Validate must not change the request.
			*test.rank = 1
			if *input.ReadyRank != *test.want {
				t.Fatalf("ReadyRank aliases caller memory: %d", *input.ReadyRank)
			}
		})
	}
}

func TestCreateIssueInputValidateRejectsOutOfRangeReadyRank(t *testing.T) {
	for _, value := range []int64{-1, domain.MaxReadyRank + 1} {
		_, err := (domain.CreateIssueInput{
			Type: domain.TypeTask, Title: "queued", ReadyRank: &value,
		}).Validate()
		var domainErr *domain.Error
		if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeInvalidArgument {
			t.Fatalf("Validate(%d) error = %v, want INVALID_ARGUMENT", value, err)
		}
		if len(domainErr.Details) != 1 || domainErr.Details[0].Field != "ready_rank" || domainErr.Details[0].Code != "OUT_OF_RANGE" {
			t.Fatalf("Validate(%d) details = %#v", value, domainErr.Details)
		}
	}
}

func TestCanonicalCreateIssueRequestIncludesReadyRank(t *testing.T) {
	ranked, err := domain.CanonicalCreateIssueRequest(domain.CreateIssueInput{
		Type: domain.TypeTask, Title: "queued", Status: domain.StatusReady, ReadyRank: int64Pointer(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	unranked, err := domain.CanonicalCreateIssueRequest(domain.CreateIssueInput{
		Type: domain.TypeTask, Title: "queued", Status: domain.StatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(ranked) == string(unranked) {
		t.Fatal("canonical create request ignores ready_rank")
	}
	if !strings.Contains(string(ranked), `"ready_rank":3`) {
		t.Fatalf("canonical create request = %s", ranked)
	}
}

func TestUpdateIssueInputValidatesReadyRank(t *testing.T) {
	if _, err := (domain.UpdateIssueInput{
		IssueID: "ISSUE-1", ExpectedVersion: 1,
		Changes: domain.IssuePatch{ReadyRank: domain.OptionalInt64{Set: true, Value: int64Pointer(domain.MaxReadyRank)}},
	}).Validate(); err != nil {
		t.Fatalf("maximum ready_rank error = %v", err)
	}
	if _, err := (domain.UpdateIssueInput{
		IssueID: "ISSUE-1", ExpectedVersion: 1,
		Changes: domain.IssuePatch{ReadyRank: domain.OptionalInt64{Set: true, Value: nil}},
	}).Validate(); err != nil {
		t.Fatalf("explicit null ready_rank error = %v", err)
	}
	invalid := domain.MaxReadyRank + 1
	_, err := (domain.UpdateIssueInput{
		IssueID: "ISSUE-1", ExpectedVersion: 1,
		Changes: domain.IssuePatch{ReadyRank: domain.OptionalInt64{Set: true, Value: &invalid}},
	}).Validate()
	if !errors.Is(err, &domain.Error{Code: domain.CodeValidationError}) {
		t.Fatalf("out-of-range ready_rank error = %v, want VALIDATION_ERROR", err)
	}
}

func TestApplyIssuePatchReadyRankAbsentNullAndValueSemantics(t *testing.T) {
	rank := int64(7)
	current := domain.Issue{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Type: domain.TypeTask, Status: domain.StatusReady,
		Title: "queued", Priority: domain.PriorityMedium, Version: 1, ReadyRank: &rank,
	}

	preserved, changed, err := domain.ApplyIssuePatch(current, domain.IssuePatch{
		Title: domain.OptionalValue[string]{Set: true, Value: "renamed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if preserved.ReadyRank == nil || *preserved.ReadyRank != 7 || strings.Contains(strings.Join(changed, ","), "ready_rank") {
		t.Fatalf("absent ready_rank changed state: %#v %v", preserved.ReadyRank, changed)
	}

	cleared, changed, err := domain.ApplyIssuePatch(current, domain.IssuePatch{
		ReadyRank: domain.OptionalInt64{Set: true, Value: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.ReadyRank != nil || !containsString(changed, "ready_rank") {
		t.Fatalf("explicit null ready_rank = %#v %v, want cleared", cleared.ReadyRank, changed)
	}

	zero := int64(0)
	replaced, changed, err := domain.ApplyIssuePatch(current, domain.IssuePatch{
		ReadyRank: domain.OptionalInt64{Set: true, Value: &zero},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ReadyRank == nil || *replaced.ReadyRank != 0 || !containsString(changed, "ready_rank") {
		t.Fatalf("zero ready_rank = %#v %v, want 0", replaced.ReadyRank, changed)
	}
	// ApplyIssuePatch must copy the patch value, not alias the caller's.
	zero = 99
	if *replaced.ReadyRank != 0 {
		t.Fatalf("patched ReadyRank aliases patch memory: %d", *replaced.ReadyRank)
	}
}

func TestSessionWorktreeValidation(t *testing.T) {
	worktree := "  /tmp/wt/feature  "
	normalized, err := (domain.CreateAgentSessionInput{ClientName: "client", Worktree: &worktree}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Worktree == nil || *normalized.Worktree != "/tmp/wt/feature" {
		t.Fatalf("normalized worktree = %v", normalized.Worktree)
	}
	if normalized.Worktree == &worktree {
		t.Fatal("normalized worktree aliases input memory")
	}

	blank := "   "
	if _, err := (domain.CreateAgentSessionInput{ClientName: "client", Worktree: &blank}).Validate(); !errors.Is(err, &domain.Error{Code: domain.CodeInvalidArgument}) {
		t.Fatalf("blank worktree error = %v, want INVALID_ARGUMENT", err)
	}

	tooLong := strings.Repeat("a", domain.MaxSessionWorktreeRunes+1)
	if _, err := (domain.CreateAgentSessionInput{ClientName: "client", Worktree: &tooLong}).Validate(); !errors.Is(err, &domain.Error{Code: domain.CodeLimitExceeded}) {
		t.Fatalf("oversized worktree error = %v, want LIMIT_EXCEEDED", err)
	}

	atLimit := strings.Repeat("a", domain.MaxSessionWorktreeRunes)
	if _, err := (domain.CreateAgentSessionInput{ClientName: "client", Worktree: &atLimit}).Validate(); err != nil {
		t.Fatalf("maximum-length worktree error = %v", err)
	}
}

func TestAgentSessionCloneCopiesWorktree(t *testing.T) {
	worktree := "/tmp/wt"
	session := domain.AgentSession{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ClientName: "client", Worktree: &worktree}
	clone := session.Clone()
	if clone.Worktree == session.Worktree {
		t.Fatal("Clone shares worktree pointer")
	}
	*clone.Worktree = "/tmp/mutated-clone"
	if *session.Worktree != "/tmp/wt" {
		t.Fatalf("source worktree = %q, want isolated copy", *session.Worktree)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
