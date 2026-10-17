// Package hevy is a small client for the Hevy public API
// (https://api.hevyapp.com/docs). The API needs a Hevy Pro subscription; the
// key comes from hevy.com → Settings → Developer.
package hevy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/apierr"
)

// DefaultBase is the production API root.
const DefaultBase = "https://api.hevyapp.com/v1"

const service = "hevy"

// Object is one API object as returned.
type Object = map[string]any

// Client talks to one Hevy account.
type Client struct {
	Base   string
	apiKey string
	http   *http.Client
	// CacheDir holds the exercise-template catalog; "" disables the cache.
	CacheDir string
	// UserAgent names the client in requests.
	UserAgent string
}

// New returns a client. base "" means DefaultBase.
func New(base, apiKey string) *Client {
	if base == "" {
		base = DefaultBase
	}
	c := &Client{Base: strings.TrimRight(base, "/"), apiKey: apiKey, http: &http.Client{Timeout: 20 * time.Second}, UserAgent: "coachctl"}
	if dir, err := os.UserCacheDir(); err == nil {
		c.CacheDir = filepath.Join(dir, "coachctl")
	}
	return c
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &apierr.Error{Service: service, Detail: err.Error(), Hint: "network error; retry later"}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return &apierr.Error{Service: service, Detail: err.Error()}
	}
	if resp.StatusCode >= 300 {
		e := &apierr.Error{Service: service, Status: resp.StatusCode, Detail: apierr.Trim(data)}
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			e.Hint = "the API key was rejected; check HEVY_API_KEY (Hevy Pro → Settings → Developer)"
		case http.StatusNotFound:
			e.Hint = "no such object; check the id"
		case http.StatusTooManyRequests:
			e.Hint = "rate limited; wait a minute and retry"
		}
		return e
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &apierr.Error{Service: service, Status: resp.StatusCode, Detail: "unexpected response: " + apierr.Trim(data)}
	}
	return nil
}

// Page is one page of a paged list.
type Page struct {
	Page      int      `json:"page"`
	PageCount int      `json:"page_count"`
	Items     []Object `json:"-"`
}

func (c *Client) page(ctx context.Context, path, key string, page, size int) (Page, error) {
	var raw map[string]json.RawMessage
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s%spage=%d&pageSize=%d", path, sep, page, size), nil, &raw); err != nil {
		return Page{}, err
	}
	p := Page{}
	_ = json.Unmarshal(raw["page"], &p.Page)
	_ = json.Unmarshal(raw["page_count"], &p.PageCount)
	if err := json.Unmarshal(raw[key], &p.Items); err != nil && raw[key] != nil {
		return Page{}, &apierr.Error{Service: service, Detail: "unexpected " + key + " list"}
	}
	return p, nil
}

// Workouts returns one page of workouts, newest first (page size max 10).
func (c *Client) Workouts(ctx context.Context, page, size int) (Page, error) {
	return c.page(ctx, "/workouts", "workouts", page, size)
}

// WorkoutsSince pages through workouts until one starts before since
// (YYYY-MM-DD, compared on the start date), newest first.
func (c *Client) WorkoutsSince(ctx context.Context, since string, maxPages int) ([]Object, error) {
	var out []Object
	for page := 1; page <= maxPages; page++ {
		p, err := c.Workouts(ctx, page, 10)
		if err != nil {
			return nil, err
		}
		for _, w := range p.Items {
			start, _ := w["start_time"].(string)
			if len(start) >= 10 && start[:10] < since {
				return out, nil
			}
			out = append(out, w)
		}
		if page >= p.PageCount || len(p.Items) == 0 {
			break
		}
	}
	return out, nil
}

// Workout fetches one workout.
func (c *Client) Workout(ctx context.Context, id string) (Object, error) {
	var out Object
	err := c.do(ctx, http.MethodGet, "/workouts/"+url.PathEscape(id), nil, &out)
	return out, err
}

// ExerciseHistory returns the sets logged for one exercise template.
func (c *Client) ExerciseHistory(ctx context.Context, templateID, start, end string) ([]Object, error) {
	q := url.Values{}
	if start != "" {
		q.Set("start_date", start+"T00:00:00Z")
	}
	if end != "" {
		q.Set("end_date", end+"T23:59:59Z")
	}
	path := "/exercise_history/" + url.PathEscape(templateID)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		History []Object `json:"exercise_history"`
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out.History, err
}

// Templates returns the whole exercise-template catalog (built-in and the
// user's custom ones), cached for a day because it is ~10 pages.
func (c *Client) Templates(ctx context.Context, refresh bool) ([]Object, error) {
	cache := ""
	if c.CacheDir != "" {
		cache = filepath.Join(c.CacheDir, "hevy-templates.json")
		if info, err := os.Stat(cache); err == nil && !refresh && time.Since(info.ModTime()) < 24*time.Hour {
			if data, err := os.ReadFile(cache); err == nil {
				var all []Object
				if json.Unmarshal(data, &all) == nil && len(all) > 0 {
					return all, nil
				}
			}
		}
	}
	var all []Object
	for page := 1; ; page++ {
		p, err := c.page(ctx, "/exercise_templates", "exercise_templates", page, 100)
		if err != nil {
			return nil, err
		}
		all = append(all, p.Items...)
		if page >= p.PageCount || len(p.Items) == 0 {
			break
		}
	}
	if cache != "" {
		if data, err := json.Marshal(all); err == nil && os.MkdirAll(c.CacheDir, 0o700) == nil {
			_ = os.WriteFile(cache, data, 0o600)
		}
	}
	return all, nil
}

// Routines returns one page of routines.
func (c *Client) Routines(ctx context.Context, page, size int) (Page, error) {
	return c.page(ctx, "/routines", "routines", page, size)
}

// Folders returns one page of routine folders.
func (c *Client) Folders(ctx context.Context, page, size int) (Page, error) {
	return c.page(ctx, "/routine_folders", "routine_folders", page, size)
}

// CreateRoutine posts a routine in the API's wire format.
func (c *Client) CreateRoutine(ctx context.Context, routine Object) (Object, error) {
	var out any
	if err := c.do(ctx, http.MethodPost, "/routines", Object{"routine": routine}, &out); err != nil {
		return nil, err
	}
	return firstRoutine(out), nil
}

// firstRoutine unwraps {"routine": [...]} / {"routine": {...}} responses.
func firstRoutine(v any) Object {
	m, ok := v.(map[string]any)
	if !ok {
		return Object{}
	}
	switch r := m["routine"].(type) {
	case []any:
		if len(r) > 0 {
			if o, ok := r[0].(map[string]any); ok {
				return o
			}
		}
	case map[string]any:
		return r
	}
	return m
}

// Str reads a string field.
func Str(o Object, key string) string {
	switch v := o[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Num reads a numeric field.
func Num(o Object, key string) (float64, bool) {
	v, ok := o[key].(float64)
	return v, ok
}
