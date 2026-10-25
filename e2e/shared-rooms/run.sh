#!/usr/bin/env bash
set -euo pipefail

# Shared rooms across three real networks, driven by the real binary and the
# `moltnet room` CLI only (no hand-written pairing YAML), through a local
# relay Worker (`npx wrangler dev`). "home" shares the room, "lab" and "desk"
# join by room invite. See README.md for every assertion.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$script_dir/../.." && pwd)"
source "$script_dir/helpers.sh"

timeout_seconds="${MOLTNET_E2E_TIMEOUT_SECONDS:-60}"
run_id="$(date -u +%Y%m%d%H%M%S)-$RANDOM"
run_dir="$(mktemp -d "${MOLTNET_SHARED_ROOMS_TMP_PARENT:-${TMPDIR:-/tmp}}/moltnet-shared-rooms.XXXXXX")"
chmod 700 "$run_dir"
moltnet="$run_dir/bin/moltnet"
net_home="home-$RANDOM" net_lab="lab-$RANDOM" net_desk="desk-$RANDOM"
relay_pid="" pid_home="" pid_lab="" pid_desk=""
relay_token="relay-connect-$run_id"

cleanup() {
	local status=$?
	trap - EXIT
	if [[ $status -ne 0 ]]; then
		for file in build.log relay.log home.log lab.log desk.log; do
			[[ -f "$run_dir/$file" ]] && { printf '\n--- %s (tail) ---\n' "$file" >&2; tail -80 "$run_dir/$file" >&2; }
		done
	fi
	stop_server desk
	stop_server lab
	stop_server home
	if process_alive "$relay_pid"; then
		kill "$relay_pid" >/dev/null 2>&1 || true
		wait "$relay_pid" >/dev/null 2>&1 || true
	fi
	rm -rf "$run_dir"
	return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

for command in go node npm npx curl jq; do require_command "$command"; done
port_relay="$(free_port)" port_home="$(free_port)" port_lab="$(free_port)" port_desk="$(free_port)"
relay_url="ws://127.0.0.1:$port_relay"
export E2E_RELAY_TOKEN="$relay_token"

log "run $run_id: home=$net_home:$port_home lab=$net_lab:$port_lab desk=$net_desk:$port_desk relay=$port_relay"
(cd "$root" && go build -o "$moltnet" ./cmd/moltnet) >"$run_dir/build.log" 2>&1 || fail "build failed"
if [[ ! -d "$root/relay/node_modules" ]]; then
	(cd "$root/relay" && npm ci) >"$run_dir/relay-install.log" 2>&1 || fail "relay npm ci failed"
fi
(cd "$root/relay" && exec npx wrangler dev --local --port "$port_relay" --var "RELAY_TOKEN:$relay_token") >"$run_dir/relay.log" 2>&1 &
relay_pid=$!
wait_for_relay "http://127.0.0.1:$port_relay/"

write_config home "$net_home" "$port_home"
write_config lab "$net_lab" "$port_lab"
write_config desk "$net_desk" "$port_desk"

# --- home creates and shares the room, and invites lab ----------------------
# `room create` resolves ./Moltnet in its working directory.
(cd "$run_dir/home" && "$moltnet" room create research --member lead) >/dev/null 2>"$run_dir/room-create.log" ||
	fail "room create: $(cat "$run_dir/room-create.log")"
start_server home
"$moltnet" room share research --config "$(config_of home)" >"$run_dir/share.log" 2>&1 || fail "room share: $(cat "$run_dir/share.log")"
global="$(api home GET /v1/rooms/research/hub | jq -r .global_room_id)"
[[ "$global" == sha256:*"/research" ]] || fail "room share produced no global room id"
pass "home shared research as $global"

room_invite() { # <id> <log>
	"$moltnet" room invite research --id "$1" --relay-url "$relay_url" --relay-token-env E2E_RELAY_TOKEN \
		--config "$(config_of home)" >"$2" 2>&1 || fail "room invite $1: $(cat "$2")"
}
room_invite room-lab "$run_dir/invite-lab.log"
grep -q "home fingerprint" "$run_dir/invite-lab.log" || fail "room invite did not print the home fingerprint"
stop_server home && start_server home # room links load at start

# --- lab joins by invite ----------------------------------------------------
"$moltnet" room join "$(invite_code "$run_dir/invite-lab.log")" --config "$(config_of lab)" >"$run_dir/join-lab.log" 2>&1 ||
	fail "lab room join: $(cat "$run_dir/join-lab.log")"
start_server lab
wait_hub lab research ".membership_epoch == 2 and any(.members[]; .network_id == \"$net_lab\" and .trust == \"self\")"
pass "lab joined through its room invite (epoch 2)"
post lab research scout "lab before desk"
wait_text home research "lab before desk"

# --- desk joins mid-stream: lab's next message is signed under a stale epoch --
room_invite room-desk "$run_dir/invite-desk.log"
stop_server home && start_server home
"$moltnet" room join "$(invite_code "$run_dir/invite-desk.log")" --config "$(config_of desk)" >"$run_dir/join-desk.log" 2>&1 ||
	fail "desk room join: $(cat "$run_dir/join-desk.log")"
start_server desk
wait_hub home research '.membership_epoch == 3'
wait_hub lab research '.membership_epoch == 2' # lab has not heard of desk yet
post lab research scout "lab after desk joined"
wait_text desk research "lab after desk joined"
grep -q "re-signed a queued hub message under the room's new membership epoch" "$run_dir/lab.log" || fail "lab never re-signed its stale-epoch message"
[[ "$(pairing_rejected lab room-lab)" == "0" ]] || fail "the stale epoch was treated as a rejection on lab"
wait_hub lab research '.membership_epoch == 3'
pass "stale epoch: home answered 412, lab learned desk joined, re-signed, resent; desk received it"

# --- fanout and signature labels -------------------------------------------
post home research lead "home speaks"
post lab research scout "lab speaks"
for name in lab desk; do wait_text "$name" research "home speaks"; done
for name in home desk; do wait_text "$name" research "lab speaks"; done
[[ "$(label lab research "home speaks")" == verified ]] || fail "lab labels the home's message $(label lab research "home speaks")"
[[ "$(label desk research "home speaks")" == verified ]] || fail "desk labels the home's message $(label desk research "home speaks")"
[[ "$(label desk research "lab speaks")" == introduced ]] || fail "desk labels lab's message $(label desk research "lab speaks")"
introduced_by="$(messages desk research | jq -r '[.messages[] | select(.parts[0].text == "lab speaks")][0].origin.introduced_by')"
[[ "$introduced_by" == "$net_home" ]] || fail "desk says lab's key was introduced by $introduced_by"
"$moltnet" room members research --config "$(config_of desk)" >"$run_dir/members-desk.txt" 2>&1 || fail "room members failed"
grep -q "introduced by $net_home" "$run_dir/members-desk.txt" || fail "room members does not show lab as introduced: $(cat "$run_dir/members-desk.txt")"
[[ "$(label home research "lab speaks")" == unconfirmed ]] || fail "home labels lab's message $(label home research "lab speaks")"
pass "fanout to both members; desk: home verified, lab introduced by $net_home; home: lab unconfirmed (first seen on the invite's pairing)"

# --- catch-up: desk is down while the room moves on ----------------------------
stop_server desk
post home research lead "while desk was down 1"
post lab research scout "while desk was down 2"
wait_text home research "while desk was down 2"
start_server desk
wait_text desk research "while desk was down 1"
wait_text desk research "while desk was down 2"
if grep -q "hub room caught up" "$run_dir/desk.log"; then mechanism="sync from the home"; else mechanism="the home's outbox"; fi
pass "desk caught up on both messages after restarting (via $mechanism)"

# --- origin's own copy learns its room seq; sync clamps a non-member ---------
lab_token="$(invite_code "$run_dir/invite-lab.log" | node -e '
	let code = ""; process.stdin.on("data", (d) => (code += d)).on("end", () => {
		const payload = code.trim().replace(/^moltnet-invite:/, "");
		console.log(JSON.parse(Buffer.from(payload, "base64url").toString()).pairing_token);
	});')"
[[ -n "$lab_token" ]] || fail "could not read lab's room link credential from its invite"
sync_page="$(curl -sS -H "Authorization: Bearer $lab_token" "$(url_of home)/v1/hub/sync?after=0&room=$(jq -rn --arg g "$global" '$g|@uri')")"
lab_entries="$(jq '[.entries[] | select(.message != null)] | length' <<<"$sync_page")"
((lab_entries >= 6)) || fail "lab's sync page shows only $lab_entries messages"
pass "lab's own link reads the room's sync log ($lab_entries messages)"

# --- remove desk: it receives nothing new, its writes are refused ------------
"$moltnet" room remove research "$net_desk" --config "$(config_of home)" >"$run_dir/remove.log" 2>&1 || fail "room remove: $(cat "$run_dir/remove.log")"
post home research lead "after desk was removed"
wait_text lab research "after desk was removed"
post desk research ghost "desk after removal"
deadline=$((SECONDS + timeout_seconds))
while [[ "$(pairing_rejected desk room-desk)" == "0" ]] && ((SECONDS < deadline)); do sleep 0.5; done
[[ "$(pairing_rejected desk room-desk)" != "0" ]] || fail "the home never refused the removed desk's write"
sleep 2
has_text desk research "after desk was removed" && fail "removed desk received a message placed after its removal"
has_text home research "desk after removal" && fail "home stored the removed desk's message"
has_text lab research "desk after removal" && fail "lab received the removed desk's message"
pass "removed desk received nothing new and its write was refused"

# --- cross-room replay -------------------------------------------------------
(cd "$run_dir/home" && "$moltnet" room create other --member lead) >/dev/null 2>&1 || fail "room create other failed"
api home GET /v1/rooms/other >/dev/null || fail "room other was not created"
"$moltnet" room share other --config "$(config_of home)" >/dev/null 2>&1 || fail "room share other failed"
replay="$(jq -c '[.entries[] | select(.message != null and .message.origin.network_id == "'"$net_lab"'")][0].message | .receipt = null | .target.room_id = "other"' <<<"$sync_page")"
status="$(curl -sS -o "$run_dir/replay.json" -w '%{http_code}' -X POST -H "Authorization: Bearer $lab_token" -H 'Content-Type: application/json' \
	--data-binary "$replay" "$(url_of home)/v1/messages")"
[[ "$status" == 403 ]] || fail "cross-room replay answered $status: $(cat "$run_dir/replay.json")"
messages home other | jq -e '.messages | length == 0' >/dev/null || fail "the replay landed in room other"
pass "a valid lab message replayed into another shared room was refused (403)"

log "shared-rooms e2e passed"
