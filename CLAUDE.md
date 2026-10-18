# CLAUDE.md

Guidance for Claude Code sessions in this repository.

## Keep the package self-contained

coachctl is a public package that athletes install on their own machines. Write source, CLI output,
tests, docs, commit messages and this file in terms of the package's own commands, with generic
examples only (`~/coach-workspace`, `i12345`, `example.com`). Hostnames, domains, other repos'
paths, deployment recipes and descriptions of any particular machine belong in the repo that
deploys it. Test fixtures are synthetic, never someone's real coaching data.

## Commands

```sh
just ci          # gofmt check, go vet, golangci-lint, go test -race (plain where the kernel cannot race): the gate
just test        # go test ./...
just build       # ./coachctl
just snapshot    # goreleaser build of every target into dist/, no publish
just release X.Y.Z   # tag and push; CI publishes (RELEASING.md)
```

## Finding things

- `cmd/coachctl/main.go` is the binary; `internal/cli/` wires every command (one file per area).
- `internal/store/` reads and writes the record: `journal.go`, `items.go`, `metrics.go`, with
  atomic writes and a file lock in `fs.go`.
- `internal/icu`, `internal/hevy`, `internal/notify` are the API clients; `internal/apierr` is
  their error type (exit code 3). `internal/importer` reads a coaching-mcp snapshot.
- `assets.go` embeds `skill/` and `workspace-template/`, so the binary always ships the matching
  skill, setup guide and template.
- `docs/design.md` explains why the record, permissions and command conventions are shaped as they
  are. Read the matching section before changing one of them.

## Conventions

- **Every command follows the agent-CLI contract**: concise text by default, `--json` for scripts,
  errors on stderr that say how to fix the call, exit codes 0/1/2/3 (`internal/cli/root.go`), no
  prompts, `--dry-run` on every external write, `--help` with an example.
- **The CLI is the only writer of the journal, open items and metrics.** Every write goes through
  `internal/store`, takes the lock, and replaces the file atomically. Prose files belong to the
  agent; coachctl writes them only on import.
- **Ids are global and permanent** (journal, items, metric points). Never renumber; import keeps
  the source's ids.
- **Secrets never reach output.** Doctor reports keys as set or unset; `icu get` redacts
  secret-looking fields; transport errors never echo a URL that carries a token.
- **A command change updates its help text, `skill/SKILL.md` and README in the same commit**, since
  the skill is what agents read. `coachctl skill install` ships the skill with the binary.
- Tests never touch the network: fake external services with `httptest`, and point the client at
  it through the `*_API_BASE` keys.
- Keep dependencies few: the standard library, cobra, BurntSushi/toml.
