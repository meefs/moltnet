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

Go, Node.js 22+, npm, npx, curl, jq, sqlite3. The first run installs `relay/`
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
5. **Agent read:** lab's agent-restricted credential (`agents: [scout]`,
   no explicit room membership) reads the joined room's history.
6. **Direct pairing beside a room link:** home `pair invite`s lab and lab
   joins it; home then holds two pairings to lab. Home's and lab's next
   messages still cross over the room link (fanout and submission), with no
   rejection either way.
7. **Catch-up from sync alone:** desk is stopped, two messages and three
   ~250 KB messages (~1 MB, more than one transport response) are posted,
   home is stopped and its queued rows for desk are deleted from its SQLite
   outbox (asserted non-empty first). desk restarts and holds all five,
   logged as a sync catch-up over the relay.
8. **Sync log:** lab's own room-link credential pages through the home's
   `/v1/hub/sync` and reads exactly the 11 messages placed since it joined.
9. **Removal:** `room remove research <desk>`; a later home message reaches
   lab but not desk; desk's own write is refused by the home (counted as a
   rejection on desk) and stored nowhere.
10. **Cross-room replay:** a valid lab message from the sync page, retargeted
   at a second shared room `other`, is refused with 403 and stored nowhere.
