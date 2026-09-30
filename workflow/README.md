# AI Engineering Workflow Standard

This directory will contain the workflow standard used together with Agent Board.

The first version is intentionally based on practices already proven in real projects rather than a new theoretical agent-governance model.

## Planned structure

- `00-overview.md` — scope, principles, terminology
- `01-task-definition.md` — task goal, scope, constraints, acceptance criteria
- `02-orchestration.md` — Orchestrator responsibilities and boundaries
- `03-development.md` — Developer execution and self-check
- `04-verification.md` — Independent Verifier and PASS rules
- `05-rc-and-handoff.md` — correction loop, interruption, takeover, continuation
- `06-human-decision.md` — stop-for-decision and authoritative decisions
- `07-task-board.md` — shared board state and workflow transitions

## Baseline workflow

Task defines the work. Agent Board holds shared workflow state. Workspace/Git holds the delivery. Agent activity holds execution detail. Independent Verifier supplies completion evidence.

Normal flow:

```text
READY
  -> Developer execution
  -> verification
      -> PASS -> DONE
      -> RC   -> Developer correction -> new verification
      -> DECISION REQUIRED -> human decision -> resume or close
```

The standard remains independent of any specific coding agent, harness, model provider, or effort level.
