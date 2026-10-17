// Package store reads and writes the coaching record: plain files in one
// directory (COACH_HOME). Prose (main.md, sections/, references/) belongs to
// the agent, which edits it directly. The journal, open items and metrics are
// owned by this package: it validates every write and is their only writer.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Layout names, relative to the record root.
const (
	MainFile      = "main.md"
	SectionsDir   = "sections"
	ReferencesDir = "references"
	JournalDir    = "journal"
	ArchiveDir    = "journal/archive"
	ItemsFile     = "open-items.jsonl"
	MetricsDir    = "metrics"
	SeriesFile    = "metrics/series.toml"
)

// ErrNotFound marks a lookup of an id or name that does not exist.
var ErrNotFound = errors.New("not found")

// UsageError marks a call the caller must change (bad input, an unknown
// series). The CLI maps it to exit code 2; everything else is a failure.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// Store is one coaching record on disk.
type Store struct {
	Root string
	Loc  *time.Location
	now  func() time.Time
}

// Open returns the record at root. The directory must exist; `coachctl init`
// creates it, so a missing one is almost always a wrong COACH_HOME.
func Open(root string, loc *time.Location, now func() time.Time) (*Store, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, usagef("coaching record %s does not exist: check COACH_HOME, or create one with `coachctl init <workspace>`", root)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, usagef("COACH_HOME %s is not a directory", root)
	}
	if now == nil {
		now = time.Now
	}
	return &Store{Root: root, Loc: loc, now: now}, nil
}

// Now is the current time in the record's timezone.
func (s *Store) Now() time.Time { return s.now().In(s.Loc) }

// Today is the current calendar date in the record's timezone, YYYY-MM-DD.
func (s *Store) Today() string { return s.Now().Format(dateLayout) }

func (s *Store) path(rel string) string { return filepath.Join(s.Root, rel) }

// DocFile is one prose document of the record.
type DocFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Docs lists main.md, then sections/ and references/, each sorted by name.
func (s *Store) Docs() (main *DocFile, sections, references []DocFile, err error) {
	if info, statErr := os.Stat(s.path(MainFile)); statErr == nil {
		main = &DocFile{Path: MainFile, Bytes: info.Size()}
	}
	if sections, err = s.listMarkdown(SectionsDir); err != nil {
		return nil, nil, nil, err
	}
	if references, err = s.listMarkdown(ReferencesDir); err != nil {
		return nil, nil, nil, err
	}
	return main, sections, references, nil
}

func (s *Store) listMarkdown(dir string) ([]DocFile, error) {
	entries, err := os.ReadDir(s.path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []DocFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, DocFile{Path: dir + "/" + e.Name(), Bytes: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// WriteDoc writes a prose document. Only import uses it: sessions edit prose
// with their own file tools.
func (s *Store) WriteDoc(rel, content string) error {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return writeFileAtomic(s.path(rel), []byte(content))
}
