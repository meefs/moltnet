# Contributing

## Prerequisites

- Go 1.24+
- Node 22+ for `website/`
- Docker if you want to run the Postgres-backed test job locally

## Local Workflow

Build into `bin/` and run the core checks before opening a change:

```bash
git clone https://github.com/noopolis/moltnet.git
cd moltnet
go build -o ./bin/moltnet ./cmd/moltnet
./bin/moltnet version
go test ./...
go test -race ./...
go vet ./...
```

Always pass `-o` with a path under `bin/` or a temporary directory when building. On case-insensitive filesystems, a repo-root executable named `moltnet` can overwrite the live `Moltnet` configuration file.

Docs:

```bash
cd website
npm ci
npm run build
```

## Postgres-Backed Tests

Some store coverage runs against a real Postgres instance. The suite reads:

```bash
export MOLTNET_TEST_POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/moltnet?sslmode=disable'
```

One local setup:

```bash
docker run --rm -d \
  --name moltnet-postgres \
  -e POSTGRES_DB=moltnet \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 \
  postgres:16-alpine
```

Then run:

```bash
go test ./internal/store
```

## Config Secrets

- `Moltnet`, `MoltnetNode`, and bridge config files that contain tokens must be private (`0600` or equivalent).
- Environment-only secrets such as `MOLTNET_PAIRINGS_JSON` are supported, but they do not get filesystem permission hardening.

## Docs And Guides

- Keep `README.md` focused on operator onboarding.
- Put detailed API and protocol material in `website/src/content/docs/`.
- Keep package `AGENTS.md` guides in present tense and aligned with current code, with `CLAUDE.md` symlinks for compatibility.

## Commits

Every authored commit must be one Conventional Commits line of at most 150
characters: `type(scope)!: summary`, with scope and `!` optional. Allowed types:
feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert.
No body, trailers, Co-Authored-By, "generated with/by", or robot emoji.
Git-generated Merge, Revert, fixup!, squash! and amend! messages are accepted;
merge commits skip message validation. All commits, including merges, must have
an author email of `juancfortunatti@gmail.com`. Committers may be GitHub
`noreply@github.com`; the allowlist checks the author, not the committer.

To opt into the local hook, from a fresh clone or any linked worktree:

```sh
cd "$(git rev-parse --show-toplevel)"
node .github/scripts/ci/commit-message.mjs --install
```

Requires Node 22.19 or later, with no package install. The installer preserves
existing hooks and custom `core.hooksPath`; read its result. If it leaves an
existing setup unchanged, integrate `.github/hooks/commit-msg` into that setup
instead of replacing it. The shared dispatcher runs each worktree's own tracked
hook and fails if it is missing. Installation is opt-in and never runs on install.
The hook checks the effective Git author, including `GIT_AUTHOR_EMAIL` overrides.
`--no-verify` skips the local hook; CI still checks every PR commit in
`origin/$GITHUB_BASE_REF..HEAD` with full history, before dependency installation.
