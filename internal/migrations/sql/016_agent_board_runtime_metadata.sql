-- Agent Board V0.1 runtime metadata.
--
-- issues.ready_rank is the task's optional explicit position in the
-- human-facing READY queue. It is nullable so every pre-existing row keeps
-- today's priority/claimability/sequence order; a stored rank only affects
-- listing order while the issue's stored status is ready. The upper bound
-- mirrors domain.MaxReadyRank and stays far below the "unranked" sort
-- sentinel the issue-list query substitutes for NULL.
--
-- agent_sessions.worktree is the checked-out worktree the session is running
-- in, recorded once at session creation. It is nullable so existing sessions
-- and clients that do not report one stay valid.
ALTER TABLE issues ADD COLUMN ready_rank INTEGER
    CHECK (ready_rank IS NULL OR (ready_rank >= 0 AND ready_rank <= 1000000000));

ALTER TABLE agent_sessions ADD COLUMN worktree TEXT;
