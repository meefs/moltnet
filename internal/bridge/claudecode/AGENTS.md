# Claude Code Bridge Guide

This package adapts Moltnet attachment deliveries to the local Claude Code CLI.

## Rules

- Keep shared queue/session-store behavior in `internal/bridge/clisession`.
- Do not publish Claude stdout to Moltnet.
- Use `runtime.workspace_path` as the CLI working directory.
- Use the installed Moltnet skill for all public replies.
- `runtime.mode: attached` never runs `claude`: `attached.go` + `registry.go` resolve `runtime.session` in Claude Code's session registry and write one nudge to that session's messaging socket. Refuse sockets that are symlinks, not sockets, or not owned by the current user. Never log or return the peer token. Stamp frames with the session id, and name `from-mode="bypass"` only when the session is in bypass mode: its transcript (`transcript.go`) last recorded `bypassPermissions`, or, before the transcript records any mode, its launch flags say so (`launchmode.go`).
