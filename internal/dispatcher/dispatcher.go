package dispatcher

import (
	"context"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/flows"
)

type sessionKey struct {
	chatID int64
	userID int64
}

type Dispatcher struct {
	mu        sync.Mutex
	sessions  map[sessionKey]flows.Flow
	factories map[string]func() flows.Flow
}

func New(dictionary flows.Dictionary, store flows.Store) *Dispatcher {
	return &Dispatcher{
		sessions: make(map[sessionKey]flows.Flow),
		factories: map[string]func() flows.Flow{
			"addword":   func() flows.Flow { return flows.NewAddword(dictionary, store) },
			"words":     func() flows.Flow { return flows.NewWords(store) },
			"addsource": func() flows.Flow { return flows.NewAddsource(store) },
			"sources":   func() flows.Flow { return flows.NewSources(store) },
		},
	}
}

func (d *Dispatcher) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	key, ok := keyFromUpdate(update)
	if !ok {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	message := update.Message
	for _, entity := range message.Entities {
		if entity.Type != models.MessageEntityTypeBotCommand || entity.Offset != 0 || entity.Length <= 1 || entity.Length > len(message.Text) {
			continue
		}
		if message.Text[0] != '/' {
			continue
		}
		if create, ok := d.factories[message.Text[1:entity.Length]]; ok {
			d.sessions[key] = create()
			break
		}
	}

	flow := d.sessions[key]
	if flow == nil {
		return
	}
	if flow.Handle(ctx, b, update) {
		delete(d.sessions, key)
	}
}

func keyFromUpdate(update *models.Update) (sessionKey, bool) {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return sessionKey{}, false
	}
	return sessionKey{
		chatID: update.Message.Chat.ID,
		userID: update.Message.From.ID,
	}, true
}
