// Package telegram sends notifications through the Telegram Bot API and
// answers a few read-only commands.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"xrayscope/internal/netutil"
	"xrayscope/internal/settings"
)

const defaultAPI = "https://api.telegram.org"

// Bot talks to the Bot API using the current settings.
type Bot struct {
	globalProxy string

	mu     sync.RWMutex
	cfg    settings.Telegram
	client *http.Client
}

// New creates a bot. globalProxy is used when the Telegram settings do not
// define their own proxy.
func New(globalProxy string) *Bot { return &Bot{globalProxy: globalProxy} }

// Configure applies new settings (called on every settings change).
func (b *Bot) Configure(cfg settings.Telegram) error {
	proxyURL := strings.TrimSpace(cfg.Proxy)
	if proxyURL == "" {
		proxyURL = b.globalProxy
	}
	c, err := netutil.NewClient(netutil.Options{Proxy: proxyURL, Timeout: 40 * time.Second})
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.cfg, b.client = cfg, c
	b.mu.Unlock()
	return nil
}

// Enabled reports whether messages can be sent.
func (b *Bot) Enabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cfg.Enabled && b.cfg.Token != "" && len(b.cfg.ChatIDs) > 0
}

func (b *Bot) snapshot() (settings.Telegram, *http.Client) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cfg, b.client
}

func apiURL(cfg settings.Telegram, method string) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	if base == "" {
		base = defaultAPI
	}
	return base + "/bot" + cfg.Token + "/" + method
}

type apiResp struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (b *Bot) call(ctx context.Context, method string, params url.Values) (json.RawMessage, error) {
	cfg, client := b.snapshot()
	if cfg.Token == "" || client == nil {
		return nil, errors.New("telegram is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(cfg, method), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, redact(err, cfg.Token)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, redact(err, cfg.Token)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("telegram: unexpected response (HTTP %d)", resp.StatusCode)
	}
	if !r.OK {
		return nil, fmt.Errorf("telegram: %s", r.Description)
	}
	return r.Result, nil
}

// redact removes the bot token from error text.
func redact(err error, token string) error {
	if token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
}

// Send delivers an HTML message to every configured chat.
func (b *Bot) Send(ctx context.Context, html string) error {
	cfg, _ := b.snapshot()
	if !b.Enabled() {
		return errors.New("telegram is disabled or has no chat ids")
	}
	var firstErr error
	for _, id := range cfg.ChatIDs {
		for _, part := range split(html, 3900) {
			_, err := b.call(ctx, "sendMessage", url.Values{
				"chat_id":                  {strconv.FormatInt(id, 10)},
				"text":                     {part},
				"parse_mode":               {"HTML"},
				"disable_web_page_preview": {"true"},
			})
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// SendFile uploads a document to every configured chat.
func (b *Bot) SendFile(ctx context.Context, name string, data []byte, caption string) error {
	cfg, client := b.snapshot()
	if !b.Enabled() {
		return errors.New("telegram is disabled or has no chat ids")
	}
	var firstErr error
	for _, id := range cfg.ChatIDs {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("chat_id", strconv.FormatInt(id, 10))
		_ = w.WriteField("caption", caption)
		fw, _ := w.CreateFormFile("document", name)
		_, _ = fw.Write(data)
		_ = w.Close()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(cfg, "sendDocument"), &buf)
		if err != nil {
			return redact(err, cfg.Token)
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		resp, err := client.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = redact(err, cfg.Token)
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		var r apiResp
		if json.Unmarshal(body, &r) != nil || !r.OK {
			if firstErr == nil {
				firstErr = fmt.Errorf("telegram: %s", r.Description)
			}
		}
	}
	return firstErr
}

// Test verifies the token and sends a greeting.
func (b *Bot) Test(ctx context.Context) (string, error) {
	res, err := b.call(ctx, "getMe", url.Values{})
	if err != nil {
		return "", err
	}
	var me struct {
		Username string `json:"username"`
	}
	_ = json.Unmarshal(res, &me)
	if err := b.Send(ctx, "✅ <b>XrayScope</b> is connected."); err != nil {
		return me.Username, err
	}
	return me.Username, nil
}

// Handler answers a command and returns an HTML reply.
type Handler func(ctx context.Context, cmd string, args []string) string

// Poll long-polls for commands from the allowed chats until ctx ends.
func (b *Bot) Poll(ctx context.Context, h Handler) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		cfg, _ := b.snapshot()
		if !cfg.Enabled || !cfg.Commands || cfg.Token == "" || len(cfg.ChatIDs) == 0 {
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 35*time.Second)
		res, err := b.call(pctx, "getUpdates", url.Values{
			"offset":          {strconv.FormatInt(offset, 10)},
			"timeout":         {"25"},
			"allowed_updates": {`["message"]`},
		})
		cancel()
		if err != nil {
			if !sleep(ctx, backoff) {
				return
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		var updates []struct {
			ID      int64 `json:"update_id"`
			Message struct {
				Text string `json:"text"`
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		}
		if json.Unmarshal(res, &updates) != nil {
			continue
		}
		for _, u := range updates {
			offset = u.ID + 1
			if !allowed(cfg.ChatIDs, u.Message.Chat.ID) || !strings.HasPrefix(u.Message.Text, "/") {
				continue
			}
			fields := strings.Fields(u.Message.Text)
			cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
			if i := strings.IndexByte(cmd, '@'); i >= 0 {
				cmd = cmd[:i]
			}
			reply := h(ctx, cmd, fields[1:])
			if reply == "" {
				continue
			}
			for _, part := range split(reply, 3900) {
				_, _ = b.call(ctx, "sendMessage", url.Values{
					"chat_id":    {strconv.FormatInt(u.Message.Chat.ID, 10)},
					"text":       {part},
					"parse_mode": {"HTML"},
				})
			}
		}
	}
}

func allowed(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// split cuts text into chunks below the Telegram message limit at line
// boundaries.
func split(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	for len(s) > max {
		cut := strings.LastIndexByte(s[:max], '\n')
		if cut < max/2 {
			cut = max
		}
		out = append(out, s[:cut])
		s = strings.TrimLeft(s[cut:], "\n")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// Escape escapes text for Telegram HTML parse mode.
func Escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
