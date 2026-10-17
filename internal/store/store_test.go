package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var berlin = mustLoc("Europe/Berlin")

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// newStore returns a store whose clock advances one minute per call, so
// timestamps are distinct and predictable.
func newStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	clock := time.Date(2026, 10, 17, 9, 0, 0, 0, berlin)
	s, err := Open(root, berlin, func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenMissingRootIsUsageError(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "nope"), berlin, nil)
	var u *UsageError
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "COACH_HOME") {
		t.Fatalf("want a usage error naming COACH_HOME, got %v", err)
	}
}

func TestJournalRoundTripAndEscaping(t *testing.T) {
	s := newStore(t)
	tricky := "line one\n## #9 · 2026-01-01 10:00 +01:00\n\\## #3 · 2026-01-01 10:00 +01:00\n> ⚠ Correction 2026-01-01 10:00 +01:00: not a correction\n\n  indented"
	e1, err := s.AddJournal("\r\n" + tricky + "  \n\n")
	if err != nil {
		t.Fatal(err)
	}
	if e1.ID != 1 {
		t.Fatalf("first id = %d", e1.ID)
	}
	if _, err := s.AddJournal("second"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CorrectJournal(1, "first fix\n⚠ Correction looks like a header\n\nlast line"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CorrectJournal(1, "second fix"); err != nil {
		t.Fatal(err)
	}
	all, err := s.Journal()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 entries, got %d", len(all))
	}
	got := all[0]
	if got.Body != tricky {
		t.Errorf("body changed in the round trip:\n got %q\nwant %q", got.Body, tricky)
	}
	if len(got.Corrections) != 2 || got.Corrections[0].Text != "first fix\n⚠ Correction looks like a header\n\nlast line" || got.Corrections[1].Text != "second fix" {
		t.Errorf("corrections = %+v", got.Corrections)
	}
	if got.At.Format(stampLayout) != "2026-10-17 09:01 +02:00" {
		t.Errorf("stamp = %s", got.At.Format(stampLayout))
	}
	if _, err := os.Stat(filepath.Join(s.Root, "journal", "2026-10.md")); err != nil {
		t.Errorf("month file missing: %v", err)
	}
}

func TestJournalEmptyEntryRefused(t *testing.T) {
	s := newStore(t)
	_, err := s.AddJournal(" \n\t\n")
	var u *UsageError
	if !errors.As(err, &u) {
		t.Fatalf("want usage error, got %v", err)
	}
}

func TestJournalArchiveMovesAndKeepsID(t *testing.T) {
	s := newStore(t)
	for _, b := range []string{"a", "b", "c"} {
		if _, err := s.AddJournal(b); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ArchiveJournal(2, true); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(s.Root, "journal", "archive", "2026-10.md"))
	if err != nil || !strings.Contains(string(archived), "## #2 ·") {
		t.Fatalf("entry #2 not in the archive file: %v %q", err, archived)
	}
	active, _ := os.ReadFile(filepath.Join(s.Root, "journal", "2026-10.md"))
	if strings.Contains(string(active), "## #2 ·") {
		t.Fatal("entry #2 still in the active file")
	}
	e, err := s.AddJournal("d")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != 4 {
		t.Fatalf("next id after archiving = %d, want 4", e.ID)
	}
	if _, err := s.ArchiveJournal(2, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "journal", "archive", "2026-10.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty archive month file should be removed")
	}
	got, _ := s.JournalEntry(2)
	if got.Archived || got.Body != "b" {
		t.Fatalf("restored entry = %+v", got)
	}
	if _, err := s.ArchiveJournal(99, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive of unknown id: %v", err)
	}
}

func TestJournalDuplicateIDReported(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddJournal("x"); err != nil {
		t.Fatal(err)
	}
	dup := "# Journal archive 2026-10\n\n## #1 · 2026-10-17 09:01 +02:00\n\nx\n"
	if err := os.MkdirAll(filepath.Join(s.Root, ArchiveDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ArchiveDir, "2026-10.md"), []byte(dup), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Journal(); err == nil || !strings.Contains(err.Error(), "#1 appears twice") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestItemsDedupResolveOverdue(t *testing.T) {
	s := newStore(t)
	a, created, err := s.AddItem(NewItem{Kind: KindFlag, Content: "HRV low", DedupKey: "hrv", RelevantDate: "2026-10-16"})
	if err != nil || !created {
		t.Fatalf("add: %v %v", created, err)
	}
	b, created, err := s.AddItem(NewItem{Kind: KindFlag, Content: "HRV low again", DedupKey: "hrv"})
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("dedup: created=%v id=%d err=%v", created, b.ID, err)
	}
	if !a.Overdue(s.Today()) {
		t.Error("item dated yesterday should be overdue")
	}
	if _, err := s.ResolveItem(a.ID, StatusDone, "handled"); err != nil {
		t.Fatal(err)
	}
	c, created, err := s.AddItem(NewItem{Kind: KindFlag, Content: "HRV low, new episode", DedupKey: "hrv"})
	if err != nil || !created || c.ID != 2 {
		t.Fatalf("a resolved item must not dedup: created=%v id=%d err=%v", created, c.ID, err)
	}
	items, _ := s.Items()
	if items[0].Status != StatusDone || items[0].ResolvedNote != "handled" || items[0].Content != "HRV low" {
		t.Fatalf("resolved item = %+v", items[0])
	}
	if _, _, err := s.AddItem(NewItem{Kind: "todo", Content: "x"}); err == nil {
		t.Fatal("bad kind accepted")
	}
	if _, _, err := s.AddItem(NewItem{Kind: KindFlag, Content: "x", RelevantDate: "17.10.2026"}); err == nil {
		t.Fatal("bad date accepted")
	}
	if _, err := s.ResolveItem(42, StatusDone, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve unknown: %v", err)
	}
}

func TestMetricsStateEventStale(t *testing.T) {
	s := newStore(t)
	if _, err := s.RecordMetric(NewPoint{Series: "ftp", Value: 250}); err == nil {
		t.Fatal("recording into an undefined series must fail")
	}
	if err := s.DefineSeries(Series{Name: "ftp", Kind: KindState, Unit: "W", StaleAfterDays: 30}); err != nil {
		t.Fatal(err)
	}
	if err := s.DefineSeries(Series{Name: "ftp", Kind: KindState}); err == nil {
		t.Fatal("redefinition accepted")
	}
	if err := s.DefineSeries(Series{Name: "km", Kind: KindEvent, Unit: "km", StaleAfterDays: 3}); err == nil {
		t.Fatal("stale_after_days on an event series accepted")
	}
	r1, _ := s.RecordMetric(NewPoint{Series: "ftp", Value: 250, MeasuredAt: "2026-01-01"})
	r2, _ := s.RecordMetric(NewPoint{Series: "ftp", Value: 265, MeasuredAt: "2026-08-01"})
	if r2.Supersedes == nil || r2.Supersedes.ID != r1.Point.ID {
		t.Fatalf("265 should supersede 250: %+v", r2)
	}
	r3, _ := s.RecordMetric(NewPoint{Series: "ftp", Value: 240, MeasuredAt: "2026-05-01"})
	if r3.Historical == nil || r3.Historical.ID != r2.Point.ID {
		t.Fatalf("backdated 240 should be history behind 265: %+v", r3)
	}
	if _, err := s.RecordMetric(NewPoint{Series: "ftp", Value: 1, Unit: "kg"}); err == nil {
		t.Fatal("unit mismatch accepted")
	}
	points, _ := s.Points("ftp")
	if got := ValueAsOf(points, "2026-06-30"); got == nil || got.Value != 240 {
		t.Fatalf("as of June = %+v", got)
	}
	if got := ValueAsOf(points, "2025-12-31"); got != nil {
		t.Fatalf("before the series = %+v", got)
	}
	w := StateHistory(points)
	if w[0].ValidTo != "2026-05-01" || w[2].ValidTo != "" {
		t.Fatalf("windows = %+v", w)
	}
	stale, _ := s.Stale()
	if len(stale) != 1 || stale[0].Name != "ftp" {
		t.Fatalf("ftp from August is stale after 30 days: %+v", stale)
	}
	name, p, err := s.DeletePoint(r2.Point.ID)
	if err != nil || name != "ftp" || p.Value != 265 {
		t.Fatalf("delete: %s %+v %v", name, p, err)
	}
	r4, err := s.RecordMetric(NewPoint{Series: "ftp", Value: 270})
	if err != nil || r4.Point.ID != 4 {
		t.Fatalf("ids stay global and never reuse a max: %+v %v", r4, err)
	}
	data, _ := os.ReadFile(filepath.Join(s.Root, SeriesFile))
	if !strings.Contains(string(data), "[ftp]\nkind = \"state\"\nunit = \"W\"\nstale_after_days = 30\n") {
		t.Fatalf("series.toml = %q", data)
	}
}

func TestSeriesTomlValidation(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(filepath.Join(s.Root, MetricsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"[x]\nkind = \"gauge\"\n", "[x]\nkind = \"state\"\ncolour = \"red\"\n", "[Bad Name]\nkind = \"event\"\n"} {
		if err := os.WriteFile(filepath.Join(s.Root, SeriesFile), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SeriesDefs(); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Root, SeriesFile), []byte("# comment\n[hrv-baseline]\nkind = \"state\"\nunit = \"ms\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.DefineSeries(Series{Name: "weight", Kind: KindEvent, Unit: "kg"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(s.Root, SeriesFile))
	if !strings.HasPrefix(string(data), "# comment\n[hrv-baseline]") {
		t.Fatalf("define must append, keeping comments: %q", data)
	}
}

func TestImportRefusesNonEmptyRecord(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddJournal("existing"); err != nil {
		t.Fatal(err)
	}
	err := s.ImportJournal([]Entry{{ID: 1, At: s.Now(), Body: "x"}})
	var u *UsageError
	if !errors.As(err, &u) {
		t.Fatalf("want usage error, got %v", err)
	}
}
