package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OliverKrr/coachctl/internal/store"
)

// A synthetic snapshot: every field shape records.json can carry. Never
// real coaching data.
const records = `{
  "format": "coaching-mcp-records/1",
  "exported_at": "2026-10-17 08:00:00",
  "journal": [
    {"id": 3, "entry": "Long run felt easy.", "created_at": "2026-09-30 18:30:00", "correction": null, "corrected_at": null, "archived_at": null},
    {"id": 7, "entry": "Tempo 3x10'.\nDecided: keep the volume.", "created_at": "2026-10-01 05:15:00", "correction": "It was 3x8'.\nSecond correction.", "corrected_at": "2026-10-02 07:00:00", "archived_at": null},
    {"id": 9, "entry": "Old block summary", "created_at": "2026-08-31 22:30:00", "correction": null, "corrected_at": null, "archived_at": "2026-10-10 10:00:00"}
  ],
  "open_items": [
    {"id": 12, "kind": "commitment", "content": "If the calf is quiet, run the 6x1k", "status": "open", "source": "session", "dedup_key": null, "relevant_date": "2026-10-21", "resolved_note": null, "created_at": "2026-10-15 19:00:00", "updated_at": "2026-10-15 19:00:00"},
    {"id": 13, "kind": "flag", "content": "HRV low", "status": "done", "source": "readiness", "dedup_key": "hrv-2026-10-14", "relevant_date": "2026-10-14", "resolved_note": "recovered", "created_at": "2026-10-14 05:50:00", "updated_at": "2026-10-15 06:00:00"}
  ],
  "metric_series": [
    {"name": "ftp", "kind": "state", "stale_after_days": 120, "created_at": "2026-03-01 10:00:00"},
    {"name": "body-weight", "kind": "event", "stale_after_days": null, "created_at": "2026-03-01 10:00:00"}
  ],
  "metrics": [
    {"id": 1, "name": "ftp", "value": 255, "unit": "W", "note": null, "measured_at": "2026-03-01", "created_at": "2026-03-01 10:00:00"},
    {"id": 5, "name": "ftp", "value": 262, "unit": "W", "note": "ramp", "measured_at": "2026-09-01 07:30", "created_at": "2026-09-01 06:00:00"},
    {"id": 6, "name": "body-weight", "value": 71.4, "unit": "kg", "note": null, "measured_at": "2026-10-16 05:00:00", "created_at": "2026-10-16 05:00:00"},
    {"id": 8, "name": "Plan Adherence %", "value": 80, "unit": "%", "note": null, "measured_at": "2026-10-12", "created_at": "2026-10-12 19:00:00"}
  ],
  "routines": [{"name": "weekly-review", "cadence": "weekly", "prompt": "see file", "status": "active"}]
}`

func writeSnapshot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"SKILL.md":                  "# Coaching\n\nmain context\n",
		"sections/plan.md":          "plan section\n",
		"references/injuries.md":    "injury log\n",
		"references/patterns.md":    "patterns\n",
		"seed-manifest.json":        "{}\n",
		RecordsFile:                 records,
		"journal.md":                "# Journal (markdown copy, ignored)\n",
		"references/not-markdown.x": "ignored",
	}
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImport(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	root := t.TempDir()
	starter := "starter main\n"
	if err := os.WriteFile(filepath.Join(root, store.MainFile), []byte(starter), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(root, loc, func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, loc) })
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(s, writeSnapshot(t), false, starter)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Main || rep.Sections != 1 || rep.References != 2 || rep.Journal != 3 || rep.Archived != 1 || rep.Items != 2 || rep.Series != 3 || rep.Points != 4 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Renamed["Plan Adherence %"] != "plan-adherence" || len(rep.Routines) != 1 {
		t.Fatalf("renames/routines = %+v %+v", rep.Renamed, rep.Routines)
	}
	main, _ := os.ReadFile(filepath.Join(root, store.MainFile))
	if string(main) != "# Coaching\n\nmain context\n" {
		t.Fatalf("main.md = %q", main)
	}

	entries, err := s.Journal()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int]store.Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	if e := byID[7]; e.At.Format("2006-01-02 15:04") != "2026-10-01 07:15" || len(e.Corrections) != 1 || e.Corrections[0].Text != "It was 3x8'.\nSecond correction." {
		t.Fatalf("entry 7 = %+v", e)
	}
	if !byID[9].Archived {
		t.Fatal("entry 9 should be archived")
	}
	// 2026-08-31 22:30 UTC is 2026-09-01 00:30 in Berlin: the month file
	// follows the local date.
	if _, err := os.Stat(filepath.Join(root, "journal", "archive", "2026-09.md")); err != nil {
		t.Fatalf("archived entry month: %v", err)
	}
	if next, err := s.AddJournal("after import"); err != nil || next.ID != 10 {
		t.Fatalf("next id = %d, %v", next.ID, err)
	}

	items, _ := s.Items()
	if len(items) != 2 || items[0].ID != 12 || items[0].RelevantDate != "2026-10-21" || items[1].ResolvedNote != "recovered" {
		t.Fatalf("items = %+v", items)
	}

	defs, err := s.SeriesDefs()
	if err != nil {
		t.Fatal(err)
	}
	if d := defs["ftp"]; d.Kind != store.KindState || d.StaleAfterDays != 120 || d.Unit != "W" {
		t.Fatalf("ftp def = %+v", d)
	}
	if d := defs["plan-adherence"]; d.Kind != store.KindEvent || d.Unit != "%" {
		t.Fatalf("plan-adherence def = %+v", d)
	}
	weight, _ := s.Points("body-weight")
	if weight[0].MeasuredAt != "2026-10-16 07:00" {
		t.Fatalf("UTC default timestamp converts to local: %+v", weight[0])
	}
	ftp, _ := s.Points("ftp")
	if ftp[1].MeasuredAt != "2026-09-01 07:30" {
		t.Fatalf("user-given local minute is kept: %+v", ftp[1])
	}

	if _, err := Run(s, writeSnapshot(t), false, starter); err == nil {
		t.Fatal("a second import into a filled record must fail")
	}
}

func TestImportConflictsAndMissingRecords(t *testing.T) {
	loc := time.UTC
	root := t.TempDir()
	s, _ := store.Open(root, loc, nil)
	if err := os.MkdirAll(filepath.Join(root, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "references", "injuries.md"), []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(s, writeSnapshot(t), false, "")
	if err == nil || !strings.Contains(err.Error(), "references/injuries.md") {
		t.Fatalf("want a conflict naming the file, got %v", err)
	}
	entries, _ := s.Journal()
	if len(entries) != 0 {
		t.Fatal("a refused import must write nothing")
	}

	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(s, empty, false, ""); err == nil || !strings.Contains(err.Error(), RecordsFile) {
		t.Fatalf("want missing records.json error, got %v", err)
	}
}
