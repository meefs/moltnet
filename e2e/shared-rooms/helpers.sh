#!/usr/bin/env bash
# Helpers for the shared-rooms e2e. Callers set: run_dir, timeout_seconds,
# moltnet (the built binary), relay_pid, and the pid_* / port_* / token_*
# variables for the three networks (home, lab, desk).

log() {
	printf '[shared-rooms-e2e] %s\n' "$*" >&2
}

fail() {
	log "FAIL: $*"
	exit 1
}

pass() {
	log "PASS: $*"
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

process_alive() {
	[[ -n "${1:-}" ]] && kill -0 "$1" >/dev/null 2>&1
}

# free_port binds an ephemeral port with Node (already needed for the
# relay) and releases it, so no fixed port can collide with anything.
free_port() {
	node -e '
		const net = require("node:net");
		const server = net.createServer();
		server.listen(0, "127.0.0.1", () => {
			const port = server.address().port;
			server.close(() => { console.log(port); });
		});
	'
}

# write_config <name> <network-id> <port>: a pre-pairing server config with
# sqlite storage and one operator token; no rooms, no pairings.
write_config() {
	local name="$1" network_id="$2" port="$3"
	local dir="$run_dir/$name"
	mkdir -p "$dir"
	cat >"$dir/Moltnet" <<CONFIG
version: moltnet.v1

network:
  id: $network_id
  name: $network_id

server:
  listen_addr: "127.0.0.1:$port"
  human_ingress: true

storage:
  kind: sqlite
  sqlite:
    path: $dir/moltnet.sqlite

auth:
  mode: bearer
  tokens:
    - id: operator
      value: operator-$network_id-$run_id
      scopes: [observe, write, admin]
CONFIG
	chmod 600 "$dir/Moltnet"
}

config_of() { printf '%s/%s/Moltnet' "$run_dir" "$1"; }
url_of() { local port_var="port_$1"; printf 'http://127.0.0.1:%s' "${!port_var}"; }
token_of() { local network_var="net_$1"; printf 'operator-%s-%s' "${!network_var}" "$run_id"; }

# start_server <name>: starts the network in the background, sets pid_<name>.
start_server() {
	local name="$1"
	(
		exec env -i PATH="$PATH" HOME="$HOME" MOLTNET_CONFIG="$(config_of "$name")" "$moltnet" start
	) >>"$run_dir/$name.log" 2>&1 &
	printf -v "pid_$name" '%s' "$!"
	wait_for_http "$(url_of "$name")/healthz" "$name health" "$name"
}

# stop_server <name>: SIGTERM, then SIGKILL after 5s; only our own pid.
stop_server() {
	local name="$1" pid_var="pid_$1"
	local pid="${!pid_var:-}"
	[[ -n "$pid" ]] || return 0
	if process_alive "$pid"; then
		kill "$pid" >/dev/null 2>&1 || true
		local deadline=$((SECONDS + 5))
		while process_alive "$pid" && ((SECONDS < deadline)); do sleep 0.2; done
		process_alive "$pid" && kill -KILL "$pid" >/dev/null 2>&1 || true
	fi
	wait "$pid" >/dev/null 2>&1 || true
	printf -v "$pid_var" '%s' ""
}

wait_for_http() {
	local url="$1" label="$2" name="${3:-}"
	local deadline=$((SECONDS + timeout_seconds))
	while ((SECONDS < deadline)); do
		if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
			return 0
		fi
		if [[ -n "$name" ]]; then
			local pid_var="pid_$name"
			process_alive "${!pid_var}" || fail "$name exited while waiting for $label"
		fi
		sleep 0.5
	done
	fail "timed out waiting for $label"
}

wait_for_relay() {
	local url="$1"
	local deadline=$((SECONDS + timeout_seconds))
	while ((SECONDS < deadline)); do
		process_alive "$relay_pid" || fail "relay exited while starting"
		if [[ "$(curl -sS --max-time 1 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null)" == "404" ]]; then
			return 0
		fi
		sleep 1
	done
	fail "timed out waiting for the relay Worker"
}

api() {
	local name="$1" method="$2" path="$3"
	shift 3
	curl -sS --max-time 10 -X "$method" -H "Authorization: Bearer $(token_of "$name")" -H 'Content-Type: application/json' "$@" "$(url_of "$name")$path"
}

# post <name> <room> <agent> <text>: an agent's message, sent as the operator.
post() {
	local name="$1" room="$2" agent="$3" text="$4"
	jq -nc --arg room "$room" --arg agent "$agent" --arg text "$text" \
		'{target: {kind: "room", room_id: $room}, from: {type: "agent", id: $agent}, parts: [{kind: "text", text: $text}]}' |
		api "$name" POST /v1/messages --data-binary @- >"$run_dir/post-$name.json"
	jq -e '.accepted == true' "$run_dir/post-$name.json" >/dev/null || fail "$name could not post $text: $(cat "$run_dir/post-$name.json")"
}

messages() {
	api "$1" GET "/v1/rooms/$2/messages?limit=100"
}

# label <name> <room> <text>: the origin.signature of the message with text.
label() {
	messages "$1" "$2" | jq -r --arg text "$3" '[.messages[] | select(.parts[0].text == $text)][0] | .origin.signature // "missing"'
}

has_text() {
	messages "$1" "$2" | jq -e --arg text "$3" 'any(.messages[]; .parts[0].text == $text)' >/dev/null 2>&1
}

# wait_text <name> <room> <text>
wait_text() {
	local deadline=$((SECONDS + timeout_seconds))
	while ((SECONDS < deadline)); do
		has_text "$1" "$2" "$3" && return 0
		sleep 0.5
	done
	fail "$1 never received \"$3\" in $2"
}

# wait_hub <name> <room> <jq filter on the /hub view>
wait_hub() {
	local deadline=$((SECONDS + timeout_seconds))
	while ((SECONDS < deadline)); do
		if api "$1" GET "/v1/rooms/$2/hub" | jq -e "$3" >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.5
	done
	fail "$1's view of $2 never satisfied $3: $(api "$1" GET "/v1/rooms/$2/hub")"
}

invite_code() {
	grep -o "moltnet-invite:[^[:space:]']*" "$1" | head -n1
}

pairing_rejected() {
	api "$1" GET /v1/pairings | jq -r --arg id "$2" '[.pairings[] | select(.id == $id)][0].delivery.rejected // 0'
}
