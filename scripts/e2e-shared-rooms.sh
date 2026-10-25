#!/usr/bin/env bash
set -euo pipefail

# Opt-in: three real Moltnet networks share one room through a local relay
# Worker, driven only by the `moltnet room` CLI and HTTP. Local processes and
# temporary tokens only; no Docker or external account.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_parent="$(mktemp -d "${TMPDIR:-/tmp}/moltnet-shared-rooms-wrapper.XXXXXX")"
chmod 700 "$temp_parent"
trap 'rm -rf "$temp_parent"' EXIT
export MOLTNET_SHARED_ROOMS_TMP_PARENT="$temp_parent"
"$root/e2e/shared-rooms/run.sh"
