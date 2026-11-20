package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/AGlElectronics/learn/backend/internal/tgcatchall"
)

// Catchall mirrors every email to the Compose Telegram catch-all (dev only).
type Catchall struct {
	Inner  Sender
	TG     *tgcatchall.Client
	Module string
}

// Send implements Sender.
func (c *Catchall) Send(ctx context.Context, m Message) error {
	if c != nil && c.TG.Enabled() {
		mod := strings.TrimSpace(c.Module)
		if mod == "" {
			mod = "Learn"
		}
		intended := fmt.Sprintf("%s email → %s", mod, strings.TrimSpace(m.To))
		if sub := strings.TrimSpace(m.Subject); sub != "" {
			intended += " · " + sub
		}
		_ = c.TG.Send(ctx, intended, m.Body)
		if c.Inner == nil {
			return nil
		}
	}
	if c == nil || c.Inner == nil {
		return ErrNotConfigured
	}
	return c.Inner.Send(ctx, m)
}
