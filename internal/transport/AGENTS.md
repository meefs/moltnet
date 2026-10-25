# Transport Guide

This package exposes Moltnet over network transports.

## Responsibilities

- HTTP + JSON handlers
- SSE streams
- native attachment WebSocket support
- request/response mapping to domain services

## Non-Responsibilities

- no room policy ownership
- no storage logic
- no canonical protocol type ownership

## Rules

- Keep handlers thin.
- Map transport requests into domain calls and domain events back into wire envelopes.
- Preserve a clean boundary so future transports can be added without rewriting core logic.

## Hub Rooms

- `hub.go` serves `GET /v1/hub/control` (observe, admin, or pair: the
  service admits only networks the room's log ever admitted) and the
  admin-only `GET|POST /v1/hub/rooms`, `POST /v1/hub/rooms/control`,
  `/join`, `/sync` and `/leave`; the peer routes `GET /v1/hub/sync`,
  `POST /v1/hub/admit` and `POST /v1/hub/leave` (pair scope; the service
  checks the link); and `GET /v1/rooms/{id}/hub` for room readers. A stale
  submission answers 412 with code `stale_membership_epoch`.
