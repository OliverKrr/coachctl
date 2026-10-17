// Package notify sends the athlete a Telegram message from their own bot.
// Only sendMessage is used, so the bot can be shared with a service that
// owns its webhook.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/OliverKrr/coachctl/internal/apierr"
)

// DefaultBase is the Bot API root.
const DefaultBase = "https://api.telegram.org"

// MaxChars is Telegram's message limit, counted in UTF-16 code units.
const MaxChars = 4096

// Length counts a message the way Telegram does.
func Length(text string) int { return len(utf16.Encode([]rune(text))) }

// Telegram is a bot bound to one chat.
type Telegram struct {
	base, token, chatID string
	http                *http.Client
}

// New returns a sender. base "" means DefaultBase.
func New(base, token, chatID string) *Telegram {
	if base == "" {
		base = DefaultBase
	}
	return &Telegram{base: strings.TrimRight(base, "/"), token: token, chatID: chatID, http: &http.Client{Timeout: 20 * time.Second}}
}

// Send posts a plain-text message and returns its message id.
func (t *Telegram) Send(ctx context.Context, text string) (int64, error) {
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := t.call(ctx, "sendMessage", map[string]any{"chat_id": t.chatID, "text": text}, &out); err != nil {
		return 0, err
	}
	return out.Result.MessageID, nil
}

// Me checks the token (getMe) and returns the bot's username.
func (t *Telegram) Me(ctx context.Context) (string, error) {
	var out struct {
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	err := t.call(ctx, "getMe", map[string]any{}, &out)
	return out.Result.Username, err
}

func (t *Telegram) call(ctx context.Context, method string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		// The URL carries the token; never echo the transport error's URL.
		return &apierr.Error{Service: "telegram", Detail: "request failed (network error)", Hint: "retry later"}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return &apierr.Error{Service: "telegram", Detail: err.Error()}
	}
	if resp.StatusCode >= 300 {
		var desc struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(data, &desc)
		e := &apierr.Error{Service: "telegram", Status: resp.StatusCode, Detail: desc.Description}
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusNotFound:
			e.Hint = "the bot token was rejected; check TELEGRAM_BOT_TOKEN"
		case http.StatusBadRequest, http.StatusForbidden:
			e.Hint = "check TELEGRAM_CHAT_ID, and that the athlete has started a chat with the bot"
		}
		return e
	}
	return json.Unmarshal(data, out)
}
