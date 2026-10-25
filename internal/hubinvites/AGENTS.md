# Hub Invites Guide

This package keeps the two halves of shared-room invites on disk, beside the
network's keys (`<keys dir>/hub-invites.json`, mode 0600):

- **grants** (home side): `moltnet room invite` records which pairing may
  redeem which shared room with which role, until when. The server consumes
  a grant when the joiner's signed admission arrives over that pairing.
- **pending joins** (member side): `moltnet room join` records the invite it
  redeemed; the server finishes the join once the pairing is live and drops
  the entry.

## Rules

- Shared by the CLI and the server: every write takes an flock on
  `hub-invites.json.lock`, re-reads, and replaces the file atomically.
- A damaged file fails closed, never reads as empty.
- No secrets live here: grants authorize, the pairing credential
  authenticates.
