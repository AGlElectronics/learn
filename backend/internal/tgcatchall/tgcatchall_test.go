// Code generated from con-services shared/go/tgcatchall; DO NOT EDIT. Run scripts/sync-shared.

package tgcatchall

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFromEnvDisabledOutsideDevelopment(t *testing.T) {
	t.Parallel()
	cfg := FromEnv(func(k string) string {
		switch k {
		case EnvAppEnv:
			return "production"
		case EnvToken:
			return "1:tok"
		case EnvChatID:
			return "-1001"
		default:
			return ""
		}
	})
	if cfg.Enabled() {
		t.Fatal("catch-all must not enable outside development")
	}
	if New(cfg) != nil {
		t.Fatal("New must return nil when disabled")
	}
}

func TestFromEnvEnabledInDevelopment(t *testing.T) {
	t.Parallel()
	cfg := FromEnv(func(k string) string {
		switch k {
		case EnvAppEnv:
			return "development"
		case EnvToken:
			return "1:tok"
		case EnvChatID:
			return "-1003787521365"
		case EnvThreadID:
			return "599"
		default:
			return ""
		}
	})
	if !cfg.Enabled() {
		t.Fatal("expected enabled")
	}
	if cfg.ThreadID == nil || *cfg.ThreadID != 599 {
		t.Fatalf("thread = %#v", cfg.ThreadID)
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()
	got := Format("bob@example.org (tasks assigned)", "assigned this task to you")
	want := "[dev catch-all] intended for: bob@example.org (tasks assigned)\n\nassigned this task to you"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSendPostsPrefixedMessageAndThread(t *testing.T) {
	t.Parallel()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(srv.Close)

	thread := int64(599)
	c := New(Config{
		AppEnv: Development, Token: "t", ChatID: "-1001", ThreadID: &thread, APIURL: srv.URL,
	})
	if err := c.Send(context.Background(), "Learn · robin@example.org", "quiz due tomorrow"); err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "-1001" {
		t.Fatalf("chat_id=%#v", got["chat_id"])
	}
	if got["message_thread_id"] != float64(599) {
		t.Fatalf("thread=%#v", got["message_thread_id"])
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, "intended for: Learn · robin@example.org") || !strings.Contains(text, "quiz due tomorrow") {
		t.Fatalf("text=%q", text)
	}
}
