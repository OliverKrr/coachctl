package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Item kinds and statuses.
const (
	KindCommitment = "commitment"
	KindFlag       = "flag"

	StatusOpen      = "open"
	StatusDone      = "done"
	StatusDismissed = "dismissed"
)

// Item is an open item: a commitment the athlete made, or a flag something
// raised for the coach to surface. Items are never deleted, only resolved.
type Item struct {
	ID           int       `json:"id"`
	Kind         string    `json:"kind"`
	Status       string    `json:"status"`
	Content      string    `json:"content"`
	Source       string    `json:"source,omitempty"`
	DedupKey     string    `json:"dedup_key,omitempty"`
	RelevantDate string    `json:"relevant_date,omitempty"`
	ResolvedNote string    `json:"resolved_note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Overdue reports whether an open item's date has passed.
func (it Item) Overdue(today string) bool {
	return it.Status == StatusOpen && it.RelevantDate != "" && it.RelevantDate < today
}

// Items returns every item, oldest first.
func (s *Store) Items() ([]Item, error) {
	data, err := readFileIfExists(s.path(ItemsFile))
	if err != nil {
		return nil, err
	}
	var items []Item
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	seen := map[int]bool{}
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var it Item
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", ItemsFile, line, err)
		}
		if err := validateItem(it); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", ItemsFile, line, err)
		}
		if seen[it.ID] {
			return nil, fmt.Errorf("%s line %d: item #%d appears twice", ItemsFile, line, it.ID)
		}
		seen[it.ID] = true
		items = append(items, it)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func validateItem(it Item) error {
	if it.ID <= 0 {
		return fmt.Errorf("item id must be positive")
	}
	if it.Kind != KindCommitment && it.Kind != KindFlag {
		return fmt.Errorf("item #%d: kind %q is not commitment or flag", it.ID, it.Kind)
	}
	switch it.Status {
	case StatusOpen, StatusDone, StatusDismissed:
	default:
		return fmt.Errorf("item #%d: status %q is not open, done or dismissed", it.ID, it.Status)
	}
	if strings.TrimSpace(it.Content) == "" {
		return fmt.Errorf("item #%d: content is empty", it.ID)
	}
	if it.RelevantDate != "" && !dateRe.MatchString(it.RelevantDate) {
		return fmt.Errorf("item #%d: relevant_date %q is not YYYY-MM-DD", it.ID, it.RelevantDate)
	}
	return nil
}

func (s *Store) writeItems(items []Item) error {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	var b bytes.Buffer
	for _, it := range items {
		line, err := json.Marshal(it)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return writeFileAtomic(s.path(ItemsFile), b.Bytes())
}

// NewItem is the input of AddItem.
type NewItem struct {
	Kind         string
	Content      string
	Source       string
	DedupKey     string
	RelevantDate string
}

// AddItem records an item. With a dedup key, an open item carrying the same
// key is returned instead (created=false), so a recurring condition is raised
// once.
func (s *Store) AddItem(in NewItem) (it Item, created bool, err error) {
	in.Content = normalizeText(in.Content)
	if in.Content == "" {
		return Item{}, false, usagef("item text is empty")
	}
	if in.Kind != KindCommitment && in.Kind != KindFlag {
		return Item{}, false, usagef("--kind must be commitment or flag")
	}
	if in.RelevantDate != "" {
		if _, err := ParseDate(in.RelevantDate); err != nil {
			return Item{}, false, err
		}
	}
	unlock, err := s.lock()
	if err != nil {
		return Item{}, false, err
	}
	defer unlock()
	items, err := s.Items()
	if err != nil {
		return Item{}, false, err
	}
	next := 1
	for _, existing := range items {
		if in.DedupKey != "" && existing.DedupKey == in.DedupKey && existing.Status == StatusOpen {
			return existing, false, nil
		}
		if existing.ID >= next {
			next = existing.ID + 1
		}
	}
	now := s.Now()
	it = Item{
		ID: next, Kind: in.Kind, Status: StatusOpen, Content: in.Content,
		Source: in.Source, DedupKey: in.DedupKey, RelevantDate: in.RelevantDate,
		CreatedAt: now, UpdatedAt: now,
	}
	return it, true, s.writeItems(append(items, it))
}

// ResolveItem closes an item as done or dismissed. A note is stored next to
// the original text, which stays verbatim.
func (s *Store) ResolveItem(id int, status, note string) (Item, error) {
	if status != StatusDone && status != StatusDismissed {
		return Item{}, usagef("status must be done or dismissed")
	}
	unlock, err := s.lock()
	if err != nil {
		return Item{}, err
	}
	defer unlock()
	items, err := s.Items()
	if err != nil {
		return Item{}, err
	}
	for i := range items {
		if items[i].ID != id {
			continue
		}
		items[i].Status = status
		if note = normalizeText(note); note != "" {
			items[i].ResolvedNote = note
		}
		items[i].UpdatedAt = s.Now()
		return items[i], s.writeItems(items)
	}
	return Item{}, fmt.Errorf("item #%d: %w", id, ErrNotFound)
}

// ImportItems writes items with their original ids into an empty list.
func (s *Store) ImportItems(items []Item) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	existing, err := s.Items()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return usagef("the record already has %d open items; import needs an empty list", len(existing))
	}
	for _, it := range items {
		if err := validateItem(it); err != nil {
			return err
		}
	}
	return s.writeItems(items)
}
