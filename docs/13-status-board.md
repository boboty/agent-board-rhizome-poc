# Status board contract

This document is the canonical contract for the shipped status board in Rhizome MCP.

## 1. Scope

The status board is a loopback-only, read-only, single-user local status display. It surfaces live project state including active work attempts (leases), blocked issues and their reasons, pending review requests, and a bounded planning graph for dependency inspection.

The board is not a hosted service, does not support authentication, multi-user access, or remote deployment, and does not accept writes from the browser or any client. It is designed for a single developer running locally to inspect project state during concurrent agent work.

## 2. Output surfaces

The status board data is surfaced through four independent rendering paths, each with its own compatibility guarantee:

- **CLI table** (`rhizome-mcp board`): ASCII table with status counts, active leases, blocked issues, and the review queue. Format is human-friendly and intentionally compact. Existing column names are stable, but columns may be added, so consumers should key off the header row rather than field positions — see §2.1 for the executor columns added to the active-attempts table.
- **CLI JSON** (`rhizome-mcp board --format json`): Complete board data as structured JSON. The JSON schema is authoritative; table rendering applies domain-specific summarization and does not carry the full scope of the JSON shape. Future schema evolution is additive; clients must tolerate unknown fields.
- **Static HTML snapshot** (`rhizome-mcp board --output PATH`): A self-contained, embeddable HTML file with inline CSS and all data needed for rendering. The snapshot is a discrete artifact; snapshots from different times are independent and do not communicate with any server. The snapshot is suitable for archival, diff, sharing, or embedding in CI reports.
- **Served board** (`rhizome-mcp board --serve`): An independent HTTP process listening at a loopback endpoint, with JSON API routes and an interactive HTML page. The served process lives outside any MCP session or command context and terminates on interrupt.

Each surface independently renders the same board result. None carry backwards compatibility burden for the others; a change to the table format is independent of the JSON schema, which is independent of the HTML rendering.

### 2.1. Active-attempt executor metadata

Every surface renders the same runtime information for one active attempt, so a
consumer can answer "who or what is running this, and where" without a second
read:

| Field | JSON key | CLI table column | HTML column |
| --- | --- | --- | --- |
| Agent label | `session_label` | `session_label` | Session label |
| Stable execution instance | `session_instance_key` | `session_instance_key` | Instance key |
| Harness / client | `session_client_name` | `session_client_name` | Client |
| Model | `session_model` | `session_model` | Model |
| Worktree | `session_worktree` | `session_worktree` | Worktree |
| Lease expiry | `lease_expires_at` | `lease_expires_at` | Lease expires |
| Session record | `session_id` | (not rendered) | (not rendered) |

The values come from a read-time join between the leased `work_attempts` row
and its `agent_sessions` row (`docs/02` §4), the same session metadata
`create_agent_session` records. `session_instance_key` is the advisory stable
execution-instance key described in `docs/02` §4.1, not an enforced identity.

Degradation is part of the contract: an attempt claimed without an
`agent_session_handle`, or a session that reported only part of its metadata,
stays on the board with the missing fields absent rather than erroring or
hiding the attempt. The JSON projection omits an absent field; the CLI table
leaves the cell empty; both HTML views render the em-dash placeholder. An
attempt is only listed while its lease is unexpired and its status is `active`,
so a finished or expired attempt is never shown as the current executor.

The CLI table is a human-facing TSV with a header row rather than a stable
column contract: adding the executor columns moved `lease_expires_at` from the
fifth field to the ninth. Positional consumers of `rhizome-mcp board` output
should switch to the JSON surface (`--format json`), whose schema is the
authoritative one and evolves additively.

Because these fields are part of the response content, they participate in the
`/api/board` semantic ETag (§4): a change to an executor's instance key, model,
or worktree changes the ETag and therefore reaches a polling board.

## 3. Routes and response shapes

All routes are GET/HEAD only. Any other HTTP method returns 405 Method Not Allowed with `Allow: GET, HEAD` header.

- **`GET /`** — HTML board page (interactive, same-origin fetch for updates via API routes)
- **`GET /search`** — HTML search page (issue search and snippet results)
- **`GET /issues/{id}`** — HTML issue-detail page (including attempts, activities, notes, and related issues)
- **`GET /api/board`** — JSON board data (supports ETag / If-None-Match for conditional refresh)
- **`GET /api/search`** — JSON search results. Query parameters: `q` (required; omitting it returns 400), `entity_type`, `limit`, `cursor`, `snippet_length`, `include_archived`. Any other query parameter is rejected.
- **`GET /api/issues/{id}`** — JSON issue detail (supports ETag / If-None-Match). The bare `/api/issues` path reaches the same handler but has no issue to resolve and returns 404.
- Anything else — 404 Not Found

If the underlying board service is unavailable, all routes return 503 Service Unavailable with body `service unavailable`.

## 4. ETag and If-None-Match semantics

ETags on `/api/board` and `/api/issues/{id}` are semantic, derived from the response content itself, not from timestamps or version counters. An unchanged board therefore yields byte-identical ETags and content.

A conditional request with an `If-None-Match` header matching the current ETag returns `304 Not Modified` with no body. This makes polling for board updates safe: if content has not changed, the response carries no data and the client's parse burden is zero.

## 5. HTTP method, Host, Origin and CSP posture

### Method enforcement

All routes accept GET and HEAD only. HEAD requests follow HTTP semantics: response headers are identical to GET, but no body is sent. POST, PUT, DELETE, and all other methods return 405 with the `Allow: GET, HEAD` header.

### Host and Origin validation

The server enforces the local trust boundary as described in [docs/08-local-http-transport.md](docs/08-local-http-transport.md), using the same validation rules as the MCP HTTP transport:

- A missing or unparseable `Host` header returns 400 Bad Request. A bare hostname with no port is unparseable in this sense, so `Host: example.com` returns 400.
- A well-formed `Host` authority that does not match the bound loopback authority returns 421 Misdirected Request — both a wrong port (`127.0.0.1:1`) and a wrong host (`example.com:<bound port>`).
- If an `Origin` header is present and does not exactly equal `scheme://<bound authority>`, the request is rejected with 403 Forbidden.
- If no `Origin` header is present (typical for browser navigation and non-browser MCP clients), the request is allowed.

Forwarded headers (`Forwarded`, `X-Forwarded-*`) are not trusted.

### Content Security Policy

Every response carries:

```
Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; script-src 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'
```

This policy prevents all external loads and blocks form submission from the page. Inline styles and scripts are permitted for the interactive board and search page. Images and fonts must be served by the same origin or inlined as data URIs.

### Other security headers

Every response also sets:

- `X-Content-Type-Options: nosniff` — Prevents MIME type sniffing.
- `Referrer-Policy: no-referrer` — Omits referrer information on all requests.
- `Cache-Control: no-store` — Prevents caching by browsers and intermediaries.

## 6. Bounded collections and truncation reporting

The board bounds four collections so responses stay predictable in size:

- **Blocked issues** — at most 100 entries.
- **Active attempts** — at most 100 entries.
- **Active reservations** — at most 100 entries.
- **Review requests** — at most 100 entries.

Each carries a boolean flag in the response's `truncation` object, surfaced in
the JSON response, the CLI table (as a `truncated` marker row), both HTML views
(as a text note below the table), and the semantic ETag. Each flag means "first
`MaxBoardCollectionLimit` shown; more exist", not a total. The flag is set when
the query that loaded the collection returned more results than the limit.

`attempt_gates` is one row per active attempt, so it shares `active_attempts`'s
flag. The workflow projection (§8) adds its own bounded reads and reports them
under `workflow.truncation` rather than the four flags above.

**Active reservations pre-filter semantics:** the `truncation.active_reservations`
flag is set from the reservation page's result *before* `filterReservationsByActiveAttempts`
runs. This is deliberate, to detect when the raw query was truncated. Consequently,
a truncated pre-filter reservation list may show fewer than 100 entries after
filtering if some reservations belong to attempts that are not currently active.
Orphaned reservation rows awaiting expiry sweep can also push a live reservation
out of the visible window.

The planning graph is bounded separately, by a node budget (default 100 nodes,
maximum 500), and it *does* report its cut: the response carries `truncated`
and a single-valued `truncation_reason` whenever a *budget* dropped something a
larger budget would have kept — `"node_limit"` for the node budget,
`"entry_point_limit"` for the entry-point cap, with `node_limit` taking
precedence when both apply (which in practice it always does, since every entry
point is also a traversal root). Deterministic selection outcomes are not
truncation and set neither field: a terminal issue dropped because no relation
edge attaches it to retained work, or excluded outright by
`include_terminal: false`.

Two properties of the graph's node selection matter to a board consumer:

- Non-terminal work — anything not `done` or `cancelled` — claims the node
  budget first. Terminal work is admitted afterwards, and only where a relation
  edge attaches it to a retained node. The board additionally requests the graph
  with terminal issues excluded outright, so finished work cannot consume the
  budget at all.
- `summary.entry_point_count` is computed over every claimable issue in the
  snapshot, not over the returned `nodes`, so truncation can never shrink how
  much claimable work a client is told exists. The consequence is that
  `entry_points` may name an issue that does not appear in `nodes` — under the
  node budget, and equally when an issue lies outside the traversal depth.
- The serialized `entry_points` list is capped at the same node budget, keeping
  the board's JSON, table and ETag payloads bounded on a project with thousands
  of claimable issues. `entry_point_count` greater than `len(entry_points)`,
  together with the truncation reason, is how a consumer sees that the list was
  capped; the kept prefix follows the deterministic snapshot order, so a live
  board poll does not see the window shuffle.

## 7. Workflow gate visibility

The board surfaces workflow-gate state (docs/02 §17) read-only, reusing the
same compact summary `get_work_context` reports so a human reading the board
and an agent reading context see identical state (ISSUE-175).

- **Board pages and JSON** carry one gate-progress row per active attempt
  (`attempt_gates`, joined to `active_attempts` by `attempt_id`): the
  enforcement point the attempt holder will hit, the frozen snapshot
  fingerprint when one supplied the requirements, requirement/satisfied
  counts, and each unmet requirement's key and reason. The collection shares
  the active-attempts bound. The HTML attempt table renders this as a
  `Gates` column — progress as text (`1/2 satisfied`, or `none apply`), with
  unmet requirement keys listed beneath. Issues without an active attempt
  are not evaluated on the board; their summary is on their issue-detail
  page.
- **Issue-detail pages and JSON** always carry the issue's full summary
  (`Gates`): the evaluated enforcement point (an active attempt evaluates
  its frozen snapshot at `complete_work_to_done`, otherwise live policies at
  `claim_work`), progress counts, and one row per unmet requirement with its
  reason and the imperative next action that clears it. A project with no
  matching policies reports `requirement_count` 0 and the page states that
  no gate requirements apply — the no-policy compatibility case.

Gate state participates in the semantic ETags of §4, so a polling client
refreshes when a requirement is satisfied even if nothing else changed. The
served board remains read-only: gate state is displayed, never mutated, from
the browser.

## 8. Workflow (Kanban) projection

The board renders a human workflow view of the same project state: six
columns, one card per issue. It is a projection, not a second status store.

- **READY** — stored `ready`, no active attempt, and no review that resolved
  to `changes_requested`.
- **IN PROGRESS** — the issue has an active work attempt.
- **VERIFYING** — stored `review`, or the issue has an active review attempt.
- **RC** — stored `ready` and the issue's most recent review request is
  `changes_requested`. `changes_requested` moves an issue back to `ready`
  (docs/09), so "ready after a failed review" is the RC state and is not
  distinguishable from unstarted READY work by status alone.
- **DECISION REQUIRED** — stored `blocked` because a review resolved to
  `blocked`, i.e. the reviewer stopped pending an authoritative decision.
- **DONE** — stored `done`.

### 8.1. Derivation rules

The stored status is the spine; a review signal only refines the two states a
review outcome can produce. Each issue is placed by the first matching rule:

1. archived → not projected;
2. `done` → DONE;
3. `cancelled` → not projected;
4. an active attempt → IN PROGRESS for a work attempt, VERIFYING for a review
   attempt (checked before the stored status, because a claimed issue keeps
   its stored status while its effective status is derived);
5. `review` → VERIFYING;
6. `blocked` → DECISION REQUIRED when the latest review resolved to `blocked`,
   otherwise not projected (an external block, not a decision request);
7. `ready` → RC when the latest review resolved to `changes_requested`,
   otherwise READY;
8. anything else → not projected.

A review state that no rule claims cannot move a card out of the column its
stored status implies. "Latest review" means the newest request by creation
time (request ID breaking ties) across every review status except
`superseded` — a superseded request always has a later successor that is read,
so it can never be the newest decision. Terminal statuses count: an issue can
be reopened (`done -> ready`) or have a request withdrawn, so an older
`changes_requested` or `blocked` request must not keep a card in RC or
DECISION REQUIRED after a later `approved`/`cancelled` decision.

### 8.2. One task, one column

Exactly one card is produced per issue, so an issue can never occupy two
columns. An issue that no column honestly describes is not guessed into one:
it is reported in the projection's unprojected list with a machine-readable
reason (`archived`, `cancelled`, `not_ready`, `externally_blocked`,
`unknown_status`) and a human sentence. A consumer therefore sees that work
was left out and why.

### 8.3. Card fields and degradation

A card carries the task identifier and title, type, priority, `is_claimable`,
and, when they exist: the READY-queue rank (rendered on READY cards, the only
column whose position it orders), the claiming session's executor attribution
(label, instance key, client, model, worktree, lease expiry), the review
request's identifier, status, target version, and timestamps with the count of
`changes_requested` rounds read, and the issue's commit/branch/pull-request
artifacts as delivery references.

Every optional field is omitted from the JSON when the underlying data did not
carry it; the HTML views render an em-dash placeholder rather than an empty
cell. The board never synthesizes a developer, verifier, or commit it did not
read: an attempt claimed without a session handle leaves the executor fields
absent, and an issue with no commit artifact has no delivery reference.

Several bounded reads can leave a card's information incomplete, and each is
reported rather than hidden under `workflow.truncation`: `ready`, `verifying`,
and `done` cover the three stored-status issue reads, `unprojected` covers the
open/blocked/cancelled read, and `review_requests` marks that a card's review
state may be older than the newest request. A card shows at most
`boardDeliveryReferenceLimit` delivery references; `delivery_overflow` marks
that some card shows fewer references than its issue has (the per-card cap or a
truncated artifact read), and `delivery_unavailable` marks that a card's
delivery references could not be read at all. A card count is therefore always
a lower-bound-safe number whenever its flag is set.

### 8.4. Surfaces

- **CLI JSON** (`rhizome-mcp board --format json`) and **`GET /api/board`**
  gain a `workflow` object with `columns`, `cards`, `unprojected`, and
  `truncation`. The addition is additive: every pre-existing field keeps its
  meaning.
- **CLI table** gains `workflow`, `workflow_cards`, and
  `workflow_unprojected` sections after `status_counts`.
- **HTML** renders the Kanban as the page's primary view on both the static
  snapshot and the served board. The status counts, active attempts, blocked
  issues, review queue, and planning graph remain as auxiliary sections.

The projection participates in the semantic ETag (§4), so a card moving
between columns reaches a polling client even when nothing else changed. The
served board stays read-only: the Kanban is displayed, never dragged or
mutated, from the browser.

An explicit verification-round ordinal is not projected. The bounded review
read can count how many `changes_requested` requests an issue has, which is
what the card shows, but the total number of verification rounds an issue has
been through is not addressable without a per-issue review history read, and
the free text a reviewer wrote when requesting changes lives on the review
outcome record, which the board's bounded read does not carry.

## 9. Process lifetime and stdout contract

The served board is an independent process started by `rhizome-mcp board --serve` and is unrelated to any MCP session context. It holds no session state and terminates cleanly on interrupt (SIGINT/SIGTERM).

On successful listen, exactly one line is written to stdout:

```
http://HOST:PORT/
```

(with trailing slash). The VS Code extension parses this line to extract the server endpoint. Treat it as a stable contract: the format must not change.

## 10. Scope exclusions

The status board explicitly does not include:

- Write operations from the browser or any remote client. The board is read-only.
  The workflow projection follows the same rule: no drag-and-drop, no column or
  READY-rank mutation, and no task creation or editing from the web board.
- Authentication or user accounts. It is local-only and has no credential or permission model.
- Remote or multi-user hosting. It is not designed for deployment on the internet or behind a reverse proxy.
- Cross-origin requests. CORS is not implemented; only same-origin requests (or requests with no Origin header) are accepted.
