package notify

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/mail"
)

type fakeSender struct {
	sent []mail.Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, m mail.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

type failingChannel struct{ calls int }

func (f *failingChannel) Name() string { return "broken" }
func (f *failingChannel) Send(context.Context, Recipient, Notice) error {
	f.calls++
	return errors.New("boom")
}

func TestEmailChannel(t *testing.T) {
	fs := &fakeSender{}
	e := NewEmail(fs)
	r := Recipient{UserID: uuid.New(), Email: "ann@example.test", DisplayName: "Ann"}
	if err := e.Send(context.Background(), r, Notice{Subject: "Quiz graded", Body: "You passed.", URL: "https://learn.test/q/1"}); err != nil {
		t.Fatal(err)
	}
	if len(fs.sent) != 1 {
		t.Fatalf("sent %d messages", len(fs.sent))
	}
	m := fs.sent[0]
	if m.To != "ann@example.test" || m.Subject != "Quiz graded" {
		t.Fatalf("unexpected message %+v", m)
	}
	for _, want := range []string{"Hi Ann,", "You passed.", "https://learn.test/q/1"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q: %q", want, m.Body)
		}
	}
}

func TestEmailChannelOverrideAndMissingAddress(t *testing.T) {
	fs := &fakeSender{}
	e := NewEmail(fs)
	r := Recipient{Email: "a@x.test", Channels: map[string]string{ChannelEmail: "b@x.test"}}
	if err := e.Send(context.Background(), r, Notice{}); err != nil {
		t.Fatal(err)
	}
	if fs.sent[0].To != "b@x.test" {
		t.Fatalf("to = %q", fs.sent[0].To)
	}
	if err := e.Send(context.Background(), Recipient{}, Notice{}); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabledChannelsAreDropped(t *testing.T) {
	var nilSMTP *mail.SMTP
	n := New(zerolog.Nop(), NewEmail(nil), NewEmail(nilSMTP), NewTelegram(TelegramConfig{}))
	if got := n.Channels(); len(got) != 0 {
		t.Fatalf("channels = %v", got)
	}
	if NewTelegram(TelegramConfig{}).Enabled() {
		t.Fatal("telegram enabled without token")
	}
}

// fakeBotAPI records sendMessage calls; chat "fail" answers an API error.
type fakeBotAPI struct {
	mu   sync.Mutex
	sent []map[string]any
	path string
}

func (f *fakeBotAPI) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.path = r.URL.Path
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if body["chat_id"] == "fail" {
		w.WriteHeader(nethttp.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
		return
	}
	f.mu.Lock()
	f.sent = append(f.sent, body)
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
}

func TestTelegramDelivery(t *testing.T) {
	api := &fakeBotAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	on := true
	tg := NewTelegram(TelegramConfig{BotToken: "123:abc", APIURL: srv.URL, Enabled: func(context.Context) bool { return on }})
	n := New(zerolog.Nop(), tg)
	notice := Notice{Subject: "Reminder", Body: "Radio basics is due tomorrow.", URL: "https://learn.test/courses/1"}

	if sent := n.Notify(context.Background(), notice, Recipient{Channels: map[string]string{ChannelTelegram: "42"}}); sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if api.path != "/bot123:abc/sendMessage" {
		t.Fatalf("path = %q", api.path)
	}
	msg := api.sent[0]
	if msg["chat_id"] != "42" || !strings.Contains(msg["text"].(string), "Radio basics is due tomorrow.") ||
		!strings.Contains(msg["text"].(string), "https://learn.test/courses/1") {
		t.Fatalf("message = %v", msg)
	}

	if sent := n.Notify(context.Background(), notice, Recipient{Email: "no-chat@x.test"}); sent != 0 {
		t.Fatalf("no chat id: sent = %d, want 0", sent)
	}
	err := tg.Send(context.Background(), Recipient{Channels: map[string]string{ChannelTelegram: "fail"}}, notice)
	if err == nil || !strings.Contains(err.Error(), "chat not found") || strings.Contains(err.Error(), "123:abc") {
		t.Fatalf("api error = %v (must not contain the token)", err)
	}

	on = false
	if sent := n.Notify(context.Background(), notice, Recipient{Channels: map[string]string{ChannelTelegram: "42"}}); sent != 0 {
		t.Fatalf("switched off: sent = %d, want 0 (not counted as delivered)", sent)
	}
	if len(api.sent) != 1 {
		t.Fatalf("switched off but %d messages reached the API", len(api.sent))
	}
}

func TestNotifierContinuesOnError(t *testing.T) {
	fs := &fakeSender{}
	broken := &failingChannel{}
	api := &fakeBotAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	tg := NewTelegram(TelegramConfig{BotToken: "123:abc", APIURL: srv.URL})
	n := New(zerolog.Nop(), broken, NewEmail(fs), tg)
	if got := n.Channels(); strings.Join(got, ",") != "broken,email,telegram" {
		t.Fatalf("channels = %v", got)
	}
	recips := []Recipient{{Email: "a@x.test", Channels: map[string]string{ChannelTelegram: "7"}}, {Email: "b@x.test"}}
	sent := n.Notify(context.Background(), Notice{Subject: "s", Body: "b"}, recips...)
	if sent != 3 { // two emails + one Telegram message (b has no chat id)
		t.Fatalf("sent = %d", sent)
	}
	if broken.calls != 2 || len(fs.sent) != 2 || len(api.sent) != 1 {
		t.Fatalf("broken calls %d, emails %d, telegram %d", broken.calls, len(fs.sent), len(api.sent))
	}
}

func TestNilNotifier(t *testing.T) {
	var n *Notifier
	if n.Notify(context.Background(), Notice{}, Recipient{}) != 0 || n.Channels() != nil {
		t.Fatal("nil notifier should be a no-op")
	}
}
