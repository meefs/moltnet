Pairing delivery retry is implemented and committed locally on `fix/pairing-delivery-retry`. Nothing was pushed or published.

**Relay path read end to end**

- `internal/rooms/messaging.go`: message acceptance, persistence, event publication, relay wake.
- `internal/rooms/relay.go` and `pairing_diagnostics.go`: origin stamping, federation/DM eligibility, compatibility checks.
- `internal/pairings/client.go`: HTTP versus relay transport selection.
- `internal/pairings/http_transport.go`: authenticated `remote_base_url` discovery and `POST /v1/messages`.
- `internal/pairings/relay_transport.go`: discovery/messages encoded as correlated relay calls.
- `internal/pairings/relay/call.go` and `dial.go`: connection state, timeout, request correlation, reconnect.
- `relay/src/server.ts`: opaque two-peer Worker forwarding; it does not retain messages.
- `internal/pairings/relay/inbound.go` and `inbound_dispatch.go`: bounded inbound dispatch through HTTP, using the originating frame credential.
- `internal/transport/http.go`: pair-scoped message route.
- `internal/rooms/sender_identity.go`, `pairing_binding.go`, and `federation_access.go`: authenticated origin binding and inbound room policy.
- `internal/store/history.go`, `sql_messages.go`, and JSON snapshot code: durable append and duplicate handling.

**Changed files**

```text
CHANGELOG.md
internal/app/app.go
internal/app/config_secrets.go
internal/app/pairing_delivery_config_test.go
internal/app/pairing_delivery_startup_test.go
internal/rooms/delivery.go
internal/rooms/delivery_ordering_test.go
internal/rooms/delivery_startup_test.go
internal/rooms/messaging.go
internal/rooms/pairing_compat_helpers_test.go
internal/rooms/pairing_compat_test.go
internal/rooms/pairing_delivery_relay_test.go
internal/rooms/pairing_delivery_test.go
internal/rooms/pairings.go
internal/rooms/relay.go
internal/rooms/relay_helpers_test.go
internal/rooms/relay_service_helpers_test.go
internal/rooms/relay_test.go
internal/rooms/service.go
internal/store/context.go
internal/store/dm_topology_test.go
internal/store/errors.go
internal/store/file_pairing_delivery.go
internal/store/file_store_snapshot.go
internal/store/history.go
internal/store/memory.go
internal/store/memory_pairing_delivery.go
internal/store/pairing_delivery.go
internal/store/pairing_delivery_persistence_test.go
internal/store/pairing_delivery_test.go
internal/store/sql_messages.go
internal/store/sql_migrations.go
internal/store/sql_open.go
internal/store/sql_open_test.go
internal/store/sql_pairing_delivery.go
internal/store/sql_pairing_delivery_state.go
internal/store/sql_pairing_migrations.go
internal/store/sql_postgres_test.go
pkg/protocol/pairings.go
pkg/protocol/types.go
web/dist/assets/index-D9aZaIOW.js
web/dist/index.html
web/src/components/Content/PairingView/DeliveryStatus.tsx
web/src/components/Content/PairingView/PairingView.tsx
web/src/hooks/usePairings.ts
web/src/lib/types.ts
web/src/test/pairing-delivery.test.tsx
website/src/content/docs/reference/pairings.md
PACKET_3_REPORT.md
```

**Design choices**

- Message acceptance, optional durable attachment event, outbound rows, and enqueue bounds commit together. A queue write failure cannot leave an acknowledged local message without its relay records.
- Memory uses one lock; JSON persists a working snapshot before swapping state; SQLite/Postgres use transactions. Migration 12 adds the outbox, pairing counters, and receive receipts. It backfills receive receipts only, never outbound history.
- Sequence numbers define per-pairing FIFO independently of message IDs and domain timestamps. One worker request runs per pairing; up to eight pairings progress concurrently.
- Failed discovery and delivery retain the head with attempts and next-attempt time. Exponential backoff uses equal jitter, starting below one second and capped below one minute by default. Network attempts have a five-second deadline; idle/reconnect polling is 250 ms.
- Startup resumes the queue after initial room configuration is applied. Shutdown cancels requests and waits for workers before closing persistence.
- Each pairing defaults to seven days / 10,000 pending records. `delivery_limits.max_age_seconds` and `max_pending` override them; zero means default. Negative/overflowing duration settings are rejected. Bounds remove oldest records atomically at enqueue and expire idle backlog during worker maintenance. Drops are logged and persist in a cumulative counter.
- Receive dedupe uses the credential-validated origin namespace and original message ID, across pairings and changed retry request IDs. Namespace normalization matches credential validation. Duplicate acknowledgments return the original stored message/event IDs.
- Existing globally unique message IDs remain the public identity contract. Collisions between distinct origin identities return HTTP 409 in either direction instead of false acceptance.
- Received pairing messages and follow imports are never queued for another hop. Sender compatibility checks compare the peer's reported network with its configured pairing identity.
- `GET /v1/pairings` exposes pending count, oldest pending age, last error, and dropped count. The console shows these optional fields, tolerates older servers, and refreshes every five seconds. Rebuilt `web/dist` is committed.

**Validation**

- Full `go test ./...` and `go vet ./...`: passed.
- Race-enabled rooms, stores, app, transport, pairing clients, and relay clients: passed.
- Two real in-process HTTP servers: actual peer listener down/up, automatic ordered backlog drain, exactly one stored copy.
- Lost acknowledgment after receiver acceptance: sender times out, retries, receiver stores once.
- Overflow: oldest records removed and counted, retained records drain in order.
- SQLite sender restart with pending records: delivered without a new message; twenty race-enabled restart repetitions passed.
- Actual Go relay client with an in-process Worker-protocol WebSocket broker: `relay is not connected`, reconnect, successful delivery.
- Bound-origin retries with changed request IDs and whitespace variants: one stored message and stable acknowledgments; forged bound network rejected; wire ID collisions rejected; no onward queue insertion.
- Memory/JSON/SQLite backend tests: FIFO, independent pairing bounds, age expiry, concurrent receive retries, durable errors/attempts/drop counters, reopen, and transactional rollback.
- Disposable local Postgres 16: race-enabled queue and existing backend integration tests passed, including receipt migration/backfill and no outbound history backfill.
- Startup readiness and changed room policy: retained backlog cannot replay under stale policy; application startup resumes eligible pending records.
- A policy-blocked head retains later messages; restoring the policy drains both in FIFO order.
- Console: 41 tests, TypeScript checks, and production build passed.
- Relay Worker: all 19 tests passed. Documentation site: all 27 pages built locally.
- Every changed source/document file is below 400 lines; `git diff --check` passed.
- No e2e harness ran. Go builds without an explicit output path were not used.

**Mutation checks**

Each mutation made its regression test fail; production code was restored or left untouched through Go overlays.

- Disable retry scheduling by moving the next attempt 24 hours away: outage recovery test fails.
- Remove receiver tuple dedupe: changed-request-ID retry test fails.
- Remove origin namespace normalization: canonical retry acknowledgment test fails.
- Remove startup resume: application restart delivery test fails.
- Start replay before configuration is ready: readiness/policy test fails.
- Restore the local-to-remote ID collision bypass: SQLite collision test fails.

**Review**

Cross-vendor review is complete. Initial review prompted the reverse-collision fix. Its head-of-line blocking finding was resolved by preserving the requested FIFO behavior, documenting its consequence, and adding a remediation test. The constructor/startup issue was fixed before re-review. The final bounded source re-review accepted the changes with no confirmed remaining P0, P1, or material P2 issue. One file-reading attempt exhausted its turn limit without a verdict; the same second review round completed from an inline source packet. The independent reviewer inspected code; test results were verified by the implementing team. Root inspection also confirmed transport URL redaction, no remote response-body exposure, no resurrection by late queue updates, and clearing of errors on successful completion. Per-cycle polling cost remains advisory.

**Limits**

- At-least-once delivery applies while records remain retained and the peer eventually accepts them. Memory storage remains ephemeral; persistent backends survive sender restart.
- A blocked head holds the entire pairing queue until recovery, configuration correction, or explicit age/overflow removal. Other pairings proceed independently.
- Strict remote arrival order cannot be promised after ambiguous timeouts: an earlier request can finish late. An already in-flight copy can also arrive after its queue record is dropped.
- Run one active sender per queue/store. This packet does not add distributed worker leases for multiple server processes sharing Postgres.
- The receiver retains its global message ID namespace; conflicting explicit IDs require correction or eventual retention expiry. Generated IDs avoid that collision in ordinary use.
- Existing configured/TOFU credential binding policy is preserved; learned unbound-token bindings remain process-local. The dedupe path grants no additional authority to a peer's claimed network ID.
- Cloudflare production infrastructure was not exercised. Tests cover the actual Go transport plus the Worker wire protocol in process and the existing Worker test suite.
- No history from before the pairing existed is backfilled.
