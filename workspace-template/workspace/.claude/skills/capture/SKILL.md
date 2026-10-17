---
name: capture
description: Routes a durable finding from this session into the coaching record in coaching/ - a number to a metric, what happened to the journal, a commitment to an open item, a method or rule into a reference file - and a repo-technical gotcha into this repo's CLAUDE.md. Use when a session produced something worth keeping, at session close, or before compaction.
---

# Capture — one record

The coaching record in `coaching/` is the single knowledge base: every session starts from it
(`coachctl session start`), git keeps its history, and routines read it too. Route each capture to
one destination:

| What was learned                                                 | Destination                                                                                                 |
| ---------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| A number about the athlete (weight, threshold, baseline, adherence) | `coachctl metric record <series> <value>`. State series supersede (thresholds), event series accumulate (volumes) |
| What happened, was decided, or was found in an analysis          | `coachctl journal add` (stdin): headline first line, then Decided / Learned / Committed / Watch for         |
| A journal entry that turned out wrong                            | `coachctl journal correct <id>` (stdin): what is actually true                                              |
| A commitment or something to watch                               | `coachctl item add --kind commitment\|flag` (`--date` when dated)                                           |
| A durable method, pattern or rule about coaching this athlete    | Edit the passage in `coaching/references/<name>.md` or `coaching/main.md`. Its tier says when to ask first  |
| Reusable analysis code or a multi-step query                     | Commit it under `scripts/` or `scripts/recipes/` and add its catalog row                                    |
| A repo-technical gotcha (toolchain, library quirk, API surprise) | The **Gotchas** section of this repo's `CLAUDE.md`: one bullet naming the mechanism                         |

## Procedure

1. State the finding in one sentence. One that cannot be stated that way is not a finding yet.
2. Pick one destination. The more structured one wins: a number is a metric, not prose.
3. Look for an existing home first: `rg -i '<words>' coaching/`, `coachctl metric list`, the
   Gotchas. Update or supersede rather than duplicate. A new metric series needs
   `coachctl metric define` first.
4. Write it, following the record's format and language rules, then commit.
5. Keep secrets, keys and other people's data out of every destination.
