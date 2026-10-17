# coachctl

A single-user coaching CLI for [Claude Code](https://code.claude.com). Your coaching record lives as
plain files in a git workspace; `coachctl` validates every write to it and talks to
[Intervals.icu](https://intervals.icu), [Hevy](https://www.hevyapp.com) and Telegram with your own
keys. A skill that ships inside the binary teaches Claude Code how to coach with it.

```
$ coachctl session start
Today: Sat 2026-10-17, 09:30 (Europe/Berlin)

## Open items (2 open, 1 OVERDUE)

#81 [commitment] (opened 2026-10-10, for 2026-10-16 — OVERDUE) If the calf is quiet, run the 6x1k
#84 [flag] (opened 2026-10-17) HRV 18 % under baseline two days running  — src: readiness

## Journal: latest 2 in full
...
```

Coaching is not limited to endurance sport: the journal, open items, metrics and prose documents
serve training, strength, nutrition, sleep or lifestyle coaching alike. Every integration is
optional.

## Why a CLI

An athlete coaching in Claude Code has a shell, a git repo and their own API keys. A CLI composes
with pipes, `jq` and scripts; its permissions can be scoped per subcommand; plain files diff in git,
which is the record's history and backup. For coaching in claude.ai chat, for several users, or
without a machine of your own, use the multi-user MCP server
[coaching-mcp](https://github.com/OliverKrr/coaching-mcp) instead; `coachctl import` moves a record
from there.

## Install

Download the binary for your platform from the
[releases](https://github.com/OliverKrr/coachctl/releases) and check it against `checksums.txt`:

```sh
curl -fsSLO https://github.com/OliverKrr/coachctl/releases/latest/download/coachctl_linux_arm64
curl -fsSL https://github.com/OliverKrr/coachctl/releases/latest/download/checksums.txt | grep coachctl_linux_arm64 | sha256sum -c
install -m 0755 coachctl_linux_arm64 ~/.local/bin/coachctl
```

Builds exist for `linux` and `darwin`, `amd64` and `arm64`. With Go 1.25+:
`go install github.com/OliverKrr/coachctl/cmd/coachctl@latest`.

## Set up

Ask Claude Code: **"run `coachctl setup` and follow it"**. The guide it prints walks through
creating the workspace (`coachctl init`), the keys file, the skill (`coachctl skill install`),
verification (`coachctl doctor --online`) and, on a machine that stays on, Remote Control and
scheduled routines.

Keys live in `~/.config/coachctl/env` (mode 0600), outside the workspace:

```sh
COACH_HOME=~/coach-workspace/coaching
COACH_TIMEZONE=Europe/Berlin
INTERVALS_API_KEY=...        # intervals.icu → Settings → Developer Settings
INTERVALS_ATHLETE_ID=i12345
HEVY_API_KEY=...             # hevy.com → Settings → Developer (needs Hevy Pro)
TELEGRAM_BOT_TOKEN=...       # @BotFather
TELEGRAM_CHAT_ID=...
```

The process environment overrides the file.

## The record

```
coaching/
  main.md                 the coaching document every session reads     (edited by the agent)
  sections/<name>.md      further sections                              (edited by the agent)
  references/<name>.md    one file per topic: zones, injuries, plans    (edited by the agent)
  journal/YYYY-MM.md      session history with stable ids               (coachctl journal)
  journal/archive/        archived entries, still readable              (coachctl journal archive)
  open-items.jsonl        commitments and flags                         (coachctl item)
  metrics/series.toml     metric series definitions                     (coachctl metric define)
  metrics/<series>.jsonl  measurements                                  (coachctl metric record)
```

The workspace template's `.claude/settings.json` denies direct edits to the CLI-owned files,
allows the record and read commands, and asks before external writes.

## Commands

| Area    | Commands                                                                                 |
| ------- | ---------------------------------------------------------------------------------------- |
| Session | `session start [--scope context\|items\|journal]`                                        |
| Journal | `journal add` (stdin), `list [--since] [--grep] [--headlines]`, `show <id>`, `correct <id>`, `archive <id>` |
| Items   | `item add --kind commitment\|flag`, `list [--overdue]`, `resolve <id> [--note] [--dismiss]` |
| Metrics | `metric define`, `record <series> <value>`, `get <series> [--as-of] [--all]`, `list`, `stale`, `delete <id>` |
| ICU     | `icu profile\|activities\|activity\|streams\|wellness\|fitness\|events\|week\|summary`, `icu get <path>` |
| ICU (w) | `icu workout push\|edit\|delete` (`--dry-run`)                                           |
| Hevy    | `hevy workouts\|workout\|history\|routines\|folders`, `hevy templates search`, `hevy routine create` (`--dry-run`) |
| Notify  | `notify` (stdin, Telegram, `--dry-run`)                                                  |
| Setup   | `setup`, `init <dir>`, `skill install`, `doctor [--online]`, `import <coaching-mcp snapshot>` |

Every command prints concise text, or JSON with `--json`; writes data with `--csv` where tabular;
reports errors on stderr with the fix; and exits 0 (ok), 1 (failure), 2 (wrong call) or 3 (the
external service failed). Nothing prompts. `coachctl <command> --help` has examples.

## Development

```sh
just ci        # gofmt, go vet, golangci-lint, go test -race
just build     # ./coachctl
just snapshot  # every release target into dist/
```

Tests never touch the network: Intervals.icu, Hevy and Telegram are faked with `httptest`. Design
notes are in [docs/design.md](docs/design.md), the release process in [RELEASING.md](RELEASING.md).

## Credits

The Intervals.icu guide in `skill/references/intervals.md` is written from the official API
documentation and the Intervals.icu forum. [IcuSync](https://forum.intervals.icu/t/icusync-claude-ai-mcp-connector-for-intervals-icu-no-technical-setup-required/126632)
is the no-setup option for claude.ai users who want an Intervals.icu connector instead.

## License

MIT
