# Routine: example check-in

An example routine prompt; delete it once you have real routines.

1. `coachctl session start --scope items` and take today's date from its first line.
2. If an open item is overdue, tell the athlete in two sentences which one and what it needs:
   send it with `coachctl notify` (text on stdin). If notify is not configured, write it as a
   journal entry with `coachctl journal add`.
3. If nothing is overdue, report nothing.
