# Shared Rooms E2E

Opt-in fixture: three real Moltnet networks share one room across a local
relay Worker (`npx wrangler dev --local`), set up only through the `moltnet
room` CLI and exercised only over HTTP. Nothing hand-writes `pairings[]`.

- **home** creates `research`, runs `moltnet room share research`, and
  mints one `moltnet room invite research` per member;
- **lab** and **desk** each run `moltnet room join <code>`; their servers
  redeem the invite on start (a signed admission over the invite's room
  link) and catch up.

## Prerequisites

Go, Node.js 22+, npm, npx, curl, jq. The first run installs `relay/`
dependencies with `npm ci` when `relay/node_modules` is missing. Every port
is random (Node binds an ephemeral port and releases it).

## Run

```bash
./scripts/e2e-shared-rooms.sh
MOLTNET_E2E_TIMEOUT_SECONDS=90 ./scripts/e2e-shared-rooms.sh
```

Each run builds `moltnet` once with `go build -o <run dir>/bin/moltnet`,
writes three configs in a fresh temporary directory, stops only the PIDs it
started (relay and three servers), and removes the directory on exit. On
failure it prints the tail of every log first.

## What it asserts

1. `room share` gives `research` a global room id `sha256:<home key>/research`.
2. `room invite` prints the home key fingerprint; `room join` on lab
   pins it as verified, and after a start lab is a member (epoch 2).
3. **Stale epoch:** desk joins (epoch 3) while lab still holds epoch 2;
   lab's next message is refused with 412, lab syncs, re-signs and resends
   (logged), nothing is counted as rejected, desk receives it.
4. **Fanout and labels:** home's and lab's messages reach both other
   networks. desk shows home's as `verified` (pinned from its invite) and
   lab's as `introduced` with `introduced_by` = home; `room members` prints
   "introduced by <home>". home shows lab's as `unconfirmed`.
5. **Catch-up:** desk is stopped, two messages are posted, desk restarts and
   holds both (reported: via sync or the home's outbox).
6. **Sync log:** lab's own room-link credential reads the home's
   `/v1/hub/sync` page with every message lab may see.
7. **Removal:** `room remove research <desk>`; a later home message reaches
   lab but not desk; desk's own write is refused by the home (counted as a
   rejection on desk) and stored nowhere.
8. **Cross-room replay:** a valid lab message from the sync page, retargeted
   at a second shared room `other`, is refused with 403 and stored nowhere.
