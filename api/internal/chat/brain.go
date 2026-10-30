// Package chat is Fritz's text brain: the same persona and the
// same memory store as the voice loop, minus ears and mouth.
// WhatsApp messages land here today; the agent's tool loop is
// the future resident. Keeping it tiny and store-backed means a
// routine learned by voice is known on WhatsApp and vice versa.
package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/francorobles/fritz/api/internal/agent"
	"github.com/francorobles/fritz/api/internal/memory"
	"github.com/francorobles/fritz/api/internal/meta"
)

// Brain answers chat messages with Muse Spark.
type Brain struct {
	Client *meta.Client // Spark (Chat)
	Store  *memory.Store

	// Confirmer, when set, intercepts a pending action's
	// confirmation ("yes") before the model runs — the same
	// Go-checked rule as the voice loop.
	Confirmer *agent.Confirmer
}

// Reply answers one user message, persisting the exchange to
// the shared store (restoring context on later restarts, and
// feeding the nightly memory consolidation).
func (b *Brain) Reply(ctx context.Context, text string) (string, error) {
	if b.Confirmer != nil {
		if handled, reply := b.Confirmer.HandleTurn(ctx, text); handled {
			if err := b.Store.AddTurn("user", text); err != nil {
				return reply, err
			}
			if err := b.Store.AddTurn("assistant", reply); err != nil {
				return reply, err
			}
			return reply, nil
		}
	}
	system := fmt.Sprintf(
		"You are Fritz, a warm desk companion. You are chatting by text message: "+
			"answer in 1-3 short sentences, no markdown, no lists, no URLs. "+
			"The current local time is %s.", time.Now().Format("Mon 2 Jan 2006, 3:04 PM"))
	if snap, err := b.Store.SnapshotText(30); err == nil && snap != "" {
		system += "\n\nWhat you remember about the user:\n" + snap
	}
	messages := []meta.Message{{Role: "system", Content: system}}
	if turns, err := b.Store.RecentTurns(20); err == nil {
		for _, t := range turns {
			messages = append(messages, meta.Message{Role: t.Role, Content: t.Content})
		}
	}
	messages = append(messages, meta.Message{Role: "user", Content: text})

	reply, err := b.Client.Chat(ctx, messages)
	if err != nil {
		return "", err
	}
	if err := b.Store.AddTurn("user", text); err != nil {
		return reply, fmt.Errorf("chat: store user turn: %w", err)
	}
	if err := b.Store.AddTurn("assistant", reply); err != nil {
		return reply, fmt.Errorf("chat: store reply turn: %w", err)
	}
	return reply, nil
}
