// Package icu is a small client for the Intervals.icu REST API
// (https://intervals.icu/api/v1/docs). Auth is HTTP Basic with the literal
// username API_KEY and the athlete's personal API key as the password.
package icu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OliverKrr/coachctl/internal/apierr"
)

// DefaultBase is the production API root.
const DefaultBase = "https://intervals.icu/api/v1"

const service = "intervals.icu"

// Client talks to one athlete's account.
type Client struct {
	Base      string
	AthleteID string
	apiKey    string
	http      *http.Client
	// UserAgent names the client: Cloudflare in front of the API challenges some
	// libraries' default agents.
	UserAgent string
	// maxRetryWait caps how long a 429 Retry-After is honoured.
	maxRetryWait time.Duration
}

// New returns a client. base "" means DefaultBase.
func New(base, athleteID, apiKey string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{
		Base:         strings.TrimRight(base, "/"),
		AthleteID:    athleteID,
		apiKey:       apiKey,
		http:         &http.Client{Timeout: 30 * time.Second},
		UserAgent:    "coachctl",
		maxRetryWait: 10 * time.Second,
	}
}

// AthletePath expands {athlete} in an API path with the configured id.
func (c *Client) AthletePath(path string) string {
	path = strings.ReplaceAll(path, "{athlete}", url.PathEscape(c.AthleteID))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimPrefix(path, "/api/v1")
}

// Get fetches path (relative to the API root) into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// GetRaw fetches path and returns the body unparsed.
func (c *Client) GetRaw(ctx context.Context, path string, query url.Values) ([]byte, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodGet, path, query, nil, &raw)
	return raw, err
}

// Send performs a write (POST, PUT, DELETE) with a JSON body.
func (c *Client) Send(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.do(ctx, method, path, query, body, out)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.Base + c.AthletePath(path)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.SetBasicAuth("API_KEY", c.apiKey)
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return &apierr.Error{Service: service, Detail: err.Error(), Hint: "network error; retry later"}
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return &apierr.Error{Service: service, Detail: readErr.Error()}
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if wait := retryAfter(resp.Header.Get("Retry-After")); wait <= c.maxRetryWait {
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if resp.StatusCode >= 300 {
			return statusError(resp.StatusCode, data)
		}
		if out == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return &apierr.Error{Service: service, Status: resp.StatusCode, Detail: "unexpected response: " + apierr.Trim(data)}
		}
		return nil
	}
}

func retryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 2 * time.Second
}

func statusError(status int, body []byte) error {
	e := &apierr.Error{Service: service, Status: status, Detail: apierr.Trim(body)}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Hint = "the API key or athlete id was rejected; check INTERVALS_API_KEY and INTERVALS_ATHLETE_ID (intervals.icu → Settings → Developer Settings)"
	case http.StatusNotFound:
		e.Hint = "no such object; check the id, or the path against the API docs"
	case http.StatusTooManyRequests:
		e.Hint = "rate limited; wait a minute and retry"
	}
	return e
}

var secretKeyRe = regexp.MustCompile(`(?i)(api_key|apikey|token|secret|password)`)

// Redact replaces the values of secret-looking keys anywhere in a JSON
// document, so the raw escape hatch never prints the account's API key.
func Redact(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("response is not JSON: %w", err)
	}
	return json.Marshal(redact(v))
}

func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if secretKeyRe.MatchString(k) && val != nil {
				if s, ok := val.(string); ok && s == "" {
					continue
				}
				t[k] = "[redacted]"
				continue
			}
			t[k] = redact(val)
		}
	case []any:
		for i := range t {
			t[i] = redact(t[i])
		}
	}
	return v
}
