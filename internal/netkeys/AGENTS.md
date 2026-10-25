# Network Keys Guide

This package owns a network's Ed25519 signing identity and its pinned peer
keys (the trust store).

## Responsibilities

- generate, persist, and load the local network signing key
- persist per-network-id key pins with a trust level (`verified`, `introduced`)
- reject a conflicting key for an already pinned network

## Rules

- Files live in a dedicated keys directory next to the network config
  (`<config dir>/.moltnet/keys/`), never in the Moltnet config itself: the
  server never writes its own config.
- `network.key` and `pins.json` are mode 0600, the directory 0700.
- Never log, print, or serialize the private key. `Identity` exposes only the
  public key, fingerprint, and a `Sign` method.
- Pins are durable and shared between the server and the `moltnet keys` CLI:
  every write takes the lock file, re-reads, and replaces the file atomically.
- v1 has no key rotation. Losing or leaking `network.key` means a new
  identity; peers must `moltnet keys forget` and `moltnet keys trust` again.
- Signing and envelope encoding live in `pkg/protocol`; this package only holds keys.
