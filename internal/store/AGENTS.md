# Store Guide

This package owns persistence interfaces and implementations.

## Responsibilities

- event persistence
- room, thread, and DM state
- membership persistence
- cursors and replay state
- artifact metadata

## Rules

- Define small storage interfaces close to the domain needs.
- Start with SQLite-friendly abstractions.
- Keep hosted-store upgrades possible without changing the protocol layer.

## Backends

- memory for tests and ephemeral local runs
- JSON snapshot storage as a compatibility path
- SQLite as the local default
- Postgres for shared or hosted deployments
- object storage integration for large artifacts later

## Hub Rooms

- `hub.go` defines `HubStore`: hub rooms, control logs, receipts, per-room
  origin seqs. All four backends implement it (`hub_memory.go`,
  `hub_file.go`, `sql_hub.go`, migration 16).
- Receipts are written inside the message append (`WithHubReceipt`) and
  checked before origin dedupe: a different receipt at a seq, a second
  placement of an event, or a broken chain is `ErrHubReceiptConflict`, never
  a duplicate. Membership epochs may not decrease along room seqs against
  the nearest stored receipt either side (any delivery order); SQL appends
  row-lock the hub room so concurrent appends see each other.
- `hub_sync.go` / `sql_hub_sync.go` (migration 17): ordered receipts for
  catch-up, messages by id, a member's forward-only sync cursor (seq and
  receipt digest; `ErrHubSyncRollback`), and receipts attached to an
  origin's own already stored copy.
