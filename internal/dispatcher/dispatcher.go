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
	mu      sync.Mutex
	sessions map[sessionKey]flows.Flow
}

func New() *Dispatcher {
	return &Dispatcher{sessions: make(map[sessionKey]flows.Flow)}
}

func (d *Dispatcher) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	key, ok := keyFromUpdate(update)
	if !ok {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	flow := d.sessions[key]
	if flow == nil {
		return
	}
	if flow.Handle(ctx, b, update) {
		delete(d.sessions, key)
	}
}

func (d *Dispatcher) HandleAddword(ctx context.Context, b *bot.Bot, update *models.Update) {
	key, ok := keyFromUpdate(update)
	if !ok {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	flow := flows.NewAddword()
	d.sessions[key] = flow
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
