package application

import (
	"context"

	"rhizome-mcp/internal/clock"
	"rhizome-mcp/internal/domain"
	"rhizome-mcp/internal/ports"
)

// WorkContextService reads validated compact issue work contexts.
type WorkContextService struct {
	repository ports.WorkContextRepository
	clock      clock.Clock
}

// NewWorkContextService composes the work-context read use case.
func NewWorkContextService(repository ports.WorkContextRepository, source clock.Clock) (*WorkContextService, error) {
	if repository == nil {
		return nil, domain.NewError(domain.CodeInvalidArgument, "work context repository is required", false)
	}
	if source == nil {
		return nil, domain.NewError(domain.CodeInvalidArgument, "work context clock is required", false)
	}
	return &WorkContextService{repository: repository, clock: source}, nil
}

// GetWorkContext validates the request, delegates the read, and clones the
// result so repository-owned mutable data cannot escape the application layer.
func (service *WorkContextService) GetWorkContext(ctx context.Context, input domain.GetWorkContextInput) (domain.WorkContext, error) {
	normalized, err := input.Validate()
	if err != nil {
		return domain.WorkContext{}, err
	}
	result, err := service.repository.GetWorkContext(ctx, ports.GetWorkContextCommand{Input: normalized, Now: service.clock.Now().UTC()})
	if err != nil {
		return domain.WorkContext{}, err
	}
	return domain.CloneWorkContext(result), nil
}

// IssueDeliveryReferences returns one issue's bounded delivery artifacts for
// the board's workflow cards: the same artifact projection get_work_context
// carries, plus whether that artifact read was truncated. It reuses the
// work-context read path rather than adding a second artifact query, so a card
// and a work context can never disagree about what was delivered.
//
// Reuse has a cost worth naming: GetWorkContext resolves the issue, its
// previous attempt, its gate summary and its reservation count regardless of
// Include, so this is a full work-context read per card, not a cheap
// artifact-only read. The board calls it once per non-READY card, bounded by
// the same collection limits as the rest of the board.
func (service *WorkContextService) IssueDeliveryReferences(ctx context.Context, issueID string) ([]domain.Artifact, bool, error) {
	result, err := service.GetWorkContext(ctx, domain.GetWorkContextInput{
		IssueID: issueID,
		Include: []domain.WorkContextInclude{domain.WorkContextIncludeArtifacts},
	})
	if err != nil {
		return nil, false, err
	}
	return result.Artifacts, result.Truncated, nil
}
