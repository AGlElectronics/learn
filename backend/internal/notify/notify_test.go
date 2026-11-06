package notify

import (
	"context"
	"errors"
	"strings"
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
	n := New(zerolog.Nop(), NewEmail(nil), NewEmail(nilSMTP), NewTelegram("", zerolog.Nop()))
	if got := n.Channels(); len(got) != 0 {
		t.Fatalf("channels = %v", got)
	}
	if NewTelegram("", zerolog.Nop()).Enabled() {
		t.Fatal("telegram enabled without token")
	}
}

func TestNotifierContinuesOnError(t *testing.T) {
	fs := &fakeSender{}
	broken := &failingChannel{}
	tg := NewTelegram("123:abc", zerolog.Nop())
	n := New(zerolog.Nop(), broken, NewEmail(fs), tg)
	if got := n.Channels(); strings.Join(got, ",") != "broken,email,telegram" {
		t.Fatalf("channels = %v", got)
	}
	recips := []Recipient{{Email: "a@x.test"}, {Email: "b@x.test"}}
	sent := n.Notify(context.Background(), Notice{Subject: "s", Body: "b"}, recips...)
	if sent != 4 { // email + telegram stub, for two recipients
		t.Fatalf("sent = %d", sent)
	}
	if broken.calls != 2 || len(fs.sent) != 2 {
		t.Fatalf("broken calls %d, emails %d", broken.calls, len(fs.sent))
	}
}

func TestNilNotifier(t *testing.T) {
	var n *Notifier
	if n.Notify(context.Background(), Notice{}, Recipient{}) != 0 || n.Channels() != nil {
		t.Fatal("nil notifier should be a no-op")
	}
}
