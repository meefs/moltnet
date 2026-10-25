# Rooms Guide

This package contains the core communication model.

## Core Concepts

- rooms
- threads
- direct messages
- membership
- mentions
- follow state

## Responsibilities

- room and thread behavior
- DM creation and routing
- membership and visibility rules
- notification policy evaluation

## Rules

- Keep rooms linear by default.
- Support threads as focused branches from messages.
- Do not let transport concerns leak into room logic.

## Causal Attribution

- `causal.go` stamps `message.accepted` (on durable, non-duplicate append)
  and `message.denied` (on a `canWriteRoom` write-policy rejection in
  `messaging.go`'s `SendMessageContext`) causal events via `causalWriter`.
- Both stamps derive `principal_id` from `causalPrincipal(ctx)`, which reads
  only `authn.Claims` on the context: `agent:<agentId>` for agent tokens,
  `operator:<credentialKey>` for static/operator credentials, and
  `system:moltnet.anonymous` when there are no claims at all. It never
  reads `request.From`, message parts, or any other caller-supplied field —
  those are claimed display identities, not authenticated ones.
- A `canWriteRoom` denial must always produce exactly one `message.denied`
  record before the caller's error is returned: deny paths are
  ledger-visible, never a silent drop. Other `enforceTargetWritePolicy`
  failures (e.g. unknown room) are not write-policy denials and are left
  unstamped.

## Signed Envelopes

- `envelope_signing.go` signs locally originated messages once, when they are
  queued for pairing delivery, and commits the envelope with the message
  (`store.WithMessageEnvelope`). The sender replays the stored bytes.
- `envelope_verification.go` verifies inbound pairing messages after
  `validateSenderIdentity`: strict parse, signature, origin, exact match with
  the delivered request, and the pin for the origin network. First contact
  pins only after authorization (`commitFirstContactPin`, never undone: a
  concurrent accepted message may rely on it): verified over a config-bound credential, else unconfirmed. Pinned
  origins may not send unsigned.
- Conflicting envelopes for one origin tuple are rejected in the store
  (`store.ErrEnvelopeConflict`, 409), never acknowledged as duplicates.
- Signature state is an HTTP-only view (`MessageSignatures`); the machine v1
  projection never carries it. For verified/introduced messages HTTP renders
  author, parts, mentions and created_at from the stored envelope, with the
  local projection's receive time and resolved mentions as `received_at` and
  `local_mentions`.
- Origin ids are trimmed once (`canonicalOriginIDs`) before verification,
  dedupe and persistence. A signed author's FQID must be the one its signed
  origin and id determine.
- Pairing credentials with no config binding are held to the network pinned
  through their pairing (`pinnedPairingNetworks`), which survives restarts.

## Hub Rooms

- `hub_roster.go` replays and verifies a hub room's home-signed control log
  (genesis, add, remove, leave, set_role, set_policy; epoch == seq).
- `hub_home.go`: genesis and control appends on the home, operator-only, and
  the control-log endpoint a member may pull once its network was admitted.
- `hub_member.go`: join (pull, verify against the home's pin, provision the
  local room) and sync (pull newer events, publish a membership notice).
- `hub_ingest.go` is the signed-envelope branch of sender validation, taken
  for pair-scoped requests carrying a hub envelope or a receipt. The ordinary
  pairing path and its gates are not relaxed for it.
  Home: link speaks for the origin AND roster key AND writer AND current
  epoch (else 412). Member: home link only AND receipt AND writer at the
  signed epoch in its own log AND no conflict with its own pins.
- `hub_place.go` signs local hub messages, assigns room seqs and receipts
  under `hubMu`, and commits message, envelope, receipt and fanout in one
  append. Stored ids of placed messages are `fed-<event id>`.
- `hub_send.go` replaces the no-onward-relay gates only for home fanout to
  current members (never the origin) and a member's own messages to its home;
  a 412 is retry-class: sync, re-sign, resend. A member always sends its
  stored bytes first and re-signs only after a 412, which the home sends only
  for an event it has not placed (a placed retry gets its placement back).
  The home reads the epoch, signs and places its own messages under `hubMu`.
- `hub_roster_cache.go` caches verified rosters per epoch, keyed by the
  control digests they verified; `hub_access.go` derives a network's
  membership per epoch (control-log and history visibility).
- `hub_sync_home.go` serves `GET /v1/hub/sync`, clamped by `hub_access`:
  receipts in order, messages only where visible, nothing after a removed
  member's last membership. `hub_sync_member.go` verifies a page's receipt
  chain from the stored cursor before storing anything, ingests entries
  through the live forward rule, attaches receipts to this network's own
  copies, and refuses rollback. `hub_sync_loop.go` syncs at start, on wake
  (gap, link up) and every `HubSyncInterval`, and runs join recovery.
- `hub_admission.go` (home) redeems room invite grants (`hubinvites`) and
  honors leaves; `hub_admission_member.go` signs admissions, leaves, and
  finishes pending joins. Joins are idempotent: the hub record is written
  before the local room, and recovery recreates a missing local room.
- `hub_view.go` serves `GET /v1/rooms/{id}/hub` (roster with key trust).
- Room links (`Pairing.Scope == "rooms"`) carry hub rooms only: no room or
  agent discovery, no DMs, no ordinary room traffic in either direction.
