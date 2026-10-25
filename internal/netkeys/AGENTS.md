# Network Keys Guide

This package owns a network's Ed25519 signing identity and its pinned peer
keys (the trust store).

## Responsibilities

- generate, persist, and load the local network signing key
- persist per-network-id key pins with a trust level (`verified`,
  `unconfirmed`, `introduced`); a verified pin replaces an unconfirmed one
- reject a conflicting key for an already pinned network

## Rules

- Files live in a dedicated keys directory next to the network config
  (`<config dir>/.moltnet/keys/`), never in the Moltnet config itself: the
  server never writes its own config.
- `network.key` and `pins.json` are mode 0600, the directory 0700.
- Never log, print, or serialize the private key. `Identity` exposes only the
  public key, fingerprint, and a `Sign` method.
- Pins are durable and shared between the server and the `moltnet keys` CLI:
  every write takes an flock on `pins.json.lock` (released by the kernel when
  the holder dies; never deleted), re-reads, and replaces the file atomically.
  The in-process mutex is taken only after the file lock.
- Reads are cached by the file's mtime, size and inode, so CLI changes still
  apply without a restart. A damaged or incomplete pins file fails closed.
- Without a durable keys directory the server does not sign at all.
- v1 has no key rotation. Losing or leaking `network.key` means a new
  identity; peers must `moltnet keys forget` and `moltnet keys trust` again.
- Signing and envelope encoding live in `pkg/protocol`; this package only holds keys.
