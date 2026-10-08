#!/usr/bin/env bash
# Checks the vendored Daimon wake-receipt schema against a published
# @noopolis/daimon package. Pass --write to refresh the vendored copy instead.
set -euo pipefail

daimon_version="${DAIMON_VERSION:-0.2.0}"
schema_path="internal/bridge/daimon/testdata/daimon.wake-receipt-status.v2.json"
workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT

package_name="$(npm pack "@noopolis/daimon@${daimon_version}" --silent --pack-destination "${workdir}")"
tar -xOzf "${workdir}/${package_name}" package/dist/runtime/contract-manifest.json > "${workdir}/manifest.json"
DAIMON_VERSION="${daimon_version}" node -e '
const fs = require("fs");
const manifest = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
const schema = manifest.activityV2ResponseSchema.properties.items.items;
const vendored = {
  source: `@noopolis/daimon@${process.env.DAIMON_VERSION} dist/runtime/contract-manifest.json activityV2ResponseSchema.properties.items.items`,
  refresh: "DAIMON_VERSION=<version> ./scripts/verify-daimon-receipt-schema.sh --write",
  schema,
};
process.stdout.write(JSON.stringify(vendored, null, 2) + "\n");
' "${workdir}/manifest.json" > "${workdir}/schema.json"

if [[ "${1:-}" == "--write" ]]; then
  cp "${workdir}/schema.json" "${schema_path}"
  printf 'wrote %s from @noopolis/daimon@%s\n' "${schema_path}" "${daimon_version}"
else
  cmp "${workdir}/schema.json" "${schema_path}"
  printf 'verified %s against @noopolis/daimon@%s\n' "${schema_path}" "${daimon_version}"
fi
