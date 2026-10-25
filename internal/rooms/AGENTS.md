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
  pins as verified; pinned origins may not send unsigned.
- Conflicting envelopes for one origin tuple are rejected in the store
  (`store.ErrEnvelopeConflict`, 409), never acknowledged as duplicates.
- Signature state is an HTTP-only view (`MessageSignatures`); the machine v1
  projection never carries it.
