#!/usr/bin/env bash
# Copied from coaching-mcp v3.9.2 (workspace-template/host/autosave.sh), MIT.
# Safety net for the workspace: sessions commit and push their own work; this catches what a dead or
# interrupted session left behind. Run from cron every 30 minutes. --no-verify is deliberate: the
# safety net must never be blocked by a failing hook.
#
# Usage: autosave.sh /path/to/workspace
set -euo pipefail
cd "${1:?workspace path required}"
git add -A
if ! git diff --cached --quiet; then
  git commit -q --no-verify -m "autosave: uncommitted session work ($(date -u +%FT%TZ))"
fi
# The workspace is pushed from several places (sessions, other machines), so this checkout can be
# behind. Rebase before pushing; a conflicted rebase aborts and leaves the merge to a live session.
git fetch -q origin
if ! git rebase -q "origin/$(git rev-parse --abbrev-ref HEAD)" >/dev/null 2>&1; then
  git rebase --abort >/dev/null 2>&1 || true
  exit 0
fi
if [ -n "$(git log --oneline '@{u}..HEAD' 2>/dev/null)" ]; then
  git push -q
fi
