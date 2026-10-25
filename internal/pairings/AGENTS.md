# Pairings Guide

This package owns remote Moltnet pairing discovery and relay helpers.

## Responsibilities

- fetch remote network metadata
- fetch remote room listings
- fetch remote agent listings
- keep remote namespaces explicit

## Non-Responsibilities

- no local room/message storage
- no UI formatting

## Rules

- keep discovery and relay logic explicit
- use explicit remote base URLs from pairing config
- do not pretend paired networks share one flat namespace
- `FetchRoomControl` and `FetchRoomSync` pull a hub room's control log and
  catch-up pages from its home; `hub_requests.go` posts admissions and
  leaves. Relay pairings allow exactly `GET /v1/hub/control`,
  `GET /v1/hub/sync`, `POST /v1/hub/admit` and `POST /v1/hub/leave` beyond
  the original routes.
