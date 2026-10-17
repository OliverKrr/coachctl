# Set up a coachctl coaching workspace

You are Claude Code on the user's own machine, and the user asked you to set up coaching with
`coachctl`. This guide is printed by `coachctl setup`, so it matches the installed version. Work
through it with the user step by step and adapt it to what you find: it describes the target, not a
script to paste.

**What the user ends up with.** A git repo (the _workspace_) holding their coaching record as plain
files in `coaching/`, the `coachctl` skill installed for Claude Code, and their own API keys in
`~/.config/coachctl/env`. Sessions coach from the record, read Intervals.icu and Hevy, and write
back through `coachctl`. Optionally, on a machine that stays on: Remote Control, so the Claude app
on their phone opens sessions in the workspace, and scheduled routines that run headless from cron.

**Rules for this setup.**

- Ask before anything outside the workspace: `sudo`, systemd, crontab, `~/.gitconfig`, files in
  `~/.claude` or `/etc`. Show the exact command or file first. When you cannot run a step (no sudo,
  an interactive prompt), hand the user the command and wait.
- Never ask the user to paste a key into the chat, and never print, commit or echo one. The user
  types keys into `~/.config/coachctl/env` themselves; you only check that they are set
  (`coachctl doctor` never prints values).
- Claude Code protects `.claude/` directories and `~/.claude/settings.json`: writing there asks for
  approval even when other edits are allowed, and an unattended run is refused. Do not work around
  a refusal; hand the user the content or command and list it in the hand-over.

## 1. Check the preconditions

Report the result to the user in one short list before you go on.

1. **coachctl is installed**: `coachctl --version`. If not, install a release binary from
   <https://github.com/OliverKrr/coachctl/releases> (linux/darwin, arm64/amd64; verify it against
   `checksums.txt`) into a directory on `PATH`, or `go install github.com/OliverKrr/coachctl/cmd/coachctl@latest`
   with Go 1.25+.
2. **Tools.** `git` is required. `uv` is needed only for analysis scripts in Python
   (`curl -LsSf https://astral.sh/uv/install.sh | sh` installs it per user).
3. **Accounts.** Which integrations does the user want? Each one is optional:
   - Intervals.icu: API key and athlete id from intervals.icu → Settings → Developer Settings.
   - Hevy: an API key from hevy.com → Settings → Developer. Needs Hevy Pro.
   - Telegram pushes: a bot from @BotFather and the user's chat id (the user sends the bot a
     message, then `https://api.telegram.org/bot<token>/getUpdates` shows `chat.id`; they open
     that URL themselves).
4. **Machine.** The OS, and whether it stays on (a server, a home box) or sleeps (a laptop).
   Remote Control and routines need Claude Code logged in with a claude.ai account.

## 2. Agree on the shape

Ask these together, offer the defaults, and continue with them if the user has no preference:

1. Workspace path. Default `~/coach-workspace`.
2. A private git remote? Recommended: the machine is the least durable part, and the record lives
   in git. The user creates an empty **private** repo; on a headless machine a deploy key with
   write access to that one repo is safer than a personal token.
3. Coaching timezone. Default the system's; the IANA name (`Europe/Berlin`) goes into the env file.
4. Remote Control and routines? Only on a machine that stays on.
5. A dedicated user account? On a machine others use or that holds other services' data, run the
   workspace as an unprivileged user (no sudo, no docker group). Every later step then runs as that
   user, in a terminal of theirs; you guide, they type.

## 3. Create the workspace and the record

1. `coachctl init <workspace>`. It writes the record skeleton (`coaching/`), the workspace
   `CLAUDE.md`, `.claude/settings.json` (command permissions and the guard on CLI-owned files),
   the capture skill, the routine runner and a recipe catalog. Existing files are kept.
2. Create `~/.config/coachctl/env` with mode 0600 and these lines; the user fills in the values in
   their editor, you never see them:

   ```sh
   COACH_HOME=<workspace>/coaching
   COACH_TIMEZONE=<Area/City>
   INTERVALS_API_KEY=
   INTERVALS_ATHLETE_ID=
   HEVY_API_KEY=
   TELEGRAM_BOT_TOKEN=
   TELEGRAM_CHAT_ID=
   ```

   Leave out the integrations the user does not want.
3. `coachctl skill install` puts the coachctl skill into `~/.claude/skills/coachctl/`. Rerun it
   after every coachctl upgrade.
4. Coming from coaching-mcp? Take a snapshot there (`coaching-mcp-snapshot <dir> --db <skill.db>`,
   or the export on the account page), then `coachctl import <dir>`. It moves the documents, the
   journal, open items and metrics with their ids. Review `coaching/metrics/series.toml` after.
   The snapshot is personal data: keep it outside the repo.
5. With `uv`: `uv sync` installs the analysis toolchain from the template's `pyproject.toml`
   (pandas, matplotlib); confirm `uv run python -c "import pandas, matplotlib"`. On a small ARM
   board this takes minutes; say so first. `uv.lock` is committed with the rest.
6. `git init -b main`, commit everything, and with a remote, add it and push. A fresh account has no
   git identity; if `git config --global user.email` is empty, ask the user for a name and email (a
   noreply address is fine) and set both. Without a remote, delete the push rules from the
   workspace `CLAUDE.md` so sessions do not try to push.

## 4. Verify the record and the keys

1. `coachctl doctor --online`. Every configured integration must say `ok`; `off` is fine for one
   the user skipped. A `FAIL` names its fix.
2. `coachctl session start` prints today's date, open items, the journal and the record files.
3. Without an import, run the onboarding: `coaching/main.md` starts with an onboarding section that
   tells the session what to ask. Offer it to the user now or in their first session.

## 5. Remote Control (machine that stays on)

Skip unless the user chose it.

1. **One-time interactive login and consent**, by the user in a terminal on the machine, as the
   workspace user: `cd <workspace> && claude`, then `/login` if needed, accept the trust dialog,
   `/exit`. Trust is per directory: trust accepted elsewhere, even in `$HOME`, does not count, and
   the service then restarts in a loop with "Workspace not trusted" in its journal. Then
   `claude remote-control --name <device name> --spawn same-dir`, answer "Enable Remote Control?"
   with `y`, wait for "Connected", Ctrl+C.
2. **Keep it running.** Linux with systemd: fill the placeholders in
   `host/claude-remote-control.service` (print it with the command below) and give the user the
   install commands: `sudo cp claude-remote-control.service /etc/systemd/system/`,
   `sudo systemctl daemon-reload`, `sudo systemctl enable --now claude-remote-control`. Verify with
   `systemctl is-active claude-remote-control` (`active`, not `activating`). The unit's `PATH` must
   include the directory that holds `coachctl`. macOS: a LaunchAgent with `KeepAlive` and
   `WorkingDirectory` set to the workspace; a sleeping Mac drops the connection.
3. The user opens the Claude app: the device appears under its name.

The host files are embedded in coachctl. Print one with
`coachctl setup --host-file claude-remote-control.service` (also `autosave.sh`,
`crontab.example`).

## 6. Routines (machine that stays on)

Skip unless the user chose them. Read the workspace's `scripts/README.md` first.

1. Write each routine as a prompt file `scripts/routines/<name>.md` with its tool grants in
   `scripts/routines/<name>.tools`, together with the user: goal, cadence, when to stay silent.
   Routines call `coachctl` through `Bash`; a headless run silently denies every tool not granted.
2. macOS lacks `flock` and `timeout`; offer `brew install flock coreutils`.
3. Run one routine by hand (`scripts/run_routine.sh <name>`) and read the tail of
   `data/routines.log`: it must end on a `DELIVERED`, `SILENT` or `FAILED` line. It is a real run
   that may write the journal or message the user; pick it with the user.
4. Install the crontab from `crontab.example` with the user's routines and paths. cron uses the
   system timezone, and Debian's cron ignores `CRON_TZ`. cron's `PATH` is minimal: the runner reads
   `COACHCTL_BIN` and `CLAUDE_BIN` from `scripts/routines/config.env`. With a git remote, also
   install `autosave.sh` (outside the repo) and its cron line. Show the crontab and ask first.

## 7. Hand over

Tell the user briefly: where the workspace is, how to start a session (`cd <workspace> && claude`,
or the device in the Claude app), what runs on a schedule, which manual steps are open, and that
`coachctl doctor` is the first check when something breaks. If a step of this guide was wrong for
this machine, offer to report it at <https://github.com/OliverKrr/coachctl/issues>, generically,
without personal data, hostnames or keys.
