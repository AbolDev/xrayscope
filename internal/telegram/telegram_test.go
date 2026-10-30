package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"xrayscope/internal/settings"
)

type fakeAPI struct {
	mu       sync.Mutex
	messages []url.Values
	updates  []string
	srv      *httptest.Server
}

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q, _ := url.ParseQuery(string(body))
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			f.messages = append(f.messages, q)
			io.WriteString(w, `{"ok":true,"result":{}}`)
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"username":"scope_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			out := "[" + strings.Join(f.updates, ",") + "]"
			f.updates = nil
			io.WriteString(w, `{"ok":true,"result":`+out+`}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"ok":false,"description":"not found"}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) sent() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.messages...)
}

func newBot(t *testing.T, f *fakeAPI, chats ...int64) *Bot {
	b := New("")
	if err := b.Configure(settings.Telegram{Enabled: true, Token: "123:SECRET", ChatIDs: chats, APIBase: f.srv.URL, Commands: true}); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSendAndTest(t *testing.T) {
	f := newFake(t)
	b := newBot(t, f, 10, 20)
	if err := b.Send(context.Background(), "<b>hi</b>"); err != nil {
		t.Fatal(err)
	}
	m := f.sent()
	if len(m) != 2 || m[0].Get("chat_id") != "10" || m[1].Get("chat_id") != "20" || m[0].Get("parse_mode") != "HTML" {
		t.Fatalf("messages = %+v", m)
	}
	name, err := b.Test(context.Background())
	if err != nil || name != "scope_bot" {
		t.Fatalf("test: %q %v", name, err)
	}
}

func TestDisabledWithoutChats(t *testing.T) {
	f := newFake(t)
	b := newBot(t, f)
	if b.Enabled() {
		t.Fatal("a bot without chat ids must not be enabled")
	}
	if err := b.Send(context.Background(), "x"); err == nil {
		t.Fatal("send must fail when disabled")
	}
}

func TestErrorsNeverLeakTheToken(t *testing.T) {
	b := New("")
	if err := b.Configure(settings.Telegram{Enabled: true, Token: "123:SECRET", ChatIDs: []int64{1}, APIBase: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	err := b.Send(context.Background(), "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error = %v", err)
	}
}

func TestSplit(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	parts := split(strings.Repeat(line, 100), 1000)
	if len(parts) < 10 {
		t.Fatalf("parts = %d", len(parts))
	}
	total := 0
	for _, p := range parts {
		if len(p) > 1000 {
			t.Fatalf("part of %d bytes", len(p))
		}
		total += strings.Count(p, "x")
	}
	if total != 9900 {
		t.Fatalf("content lost: %d", total)
	}
}

func TestPollAnswersOnlyAllowedChats(t *testing.T) {
	f := newFake(t)
	upd := func(id int, chat int64, text string) string {
		b, _ := json.Marshal(map[string]any{"update_id": id, "message": map[string]any{"text": text, "chat": map[string]any{"id": chat}}})
		return string(b)
	}
	f.updates = []string{upd(1, 10, "/online@scope_bot"), upd(2, 999, "/online"), upd(3, 10, "not a command")}
	b := newBot(t, f, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Poll(ctx, func(_ context.Context, cmd string, args []string) string { return "reply to " + cmd })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(f.sent()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	m := f.sent()
	if len(m) != 1 || m[0].Get("chat_id") != "10" || m[0].Get("text") != "reply to online" {
		t.Fatalf("replies = %+v", m)
	}
}

func TestEscape(t *testing.T) {
	if got := Escape(`a<b>&"c"`); got != `a&lt;b&gt;&amp;"c"` {
		t.Fatalf("escape = %q", got)
	}
}
