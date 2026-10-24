package telegram

import (
	"context"
	"log"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/flows"
)

type Receiver interface{ Enqueue(flows.Message) error }

func Options(receiver Receiver) []tg.Option {
	return []tg.Option{tg.WithDefaultHandler(Handler(receiver)), tg.WithNotAsyncHandlers(), tg.WithWorkers(1)}
}

func Handler(receiver Receiver) tg.HandlerFunc {
	return func(_ context.Context, _ *tg.Bot, update *models.Update) {
		if update == nil || update.Message == nil || update.Message.From == nil {
			return
		}
		m := update.Message
		message := flows.Message{ChatID: m.Chat.ID, UserID: m.From.ID, Text: m.Text}
		for _, entity := range m.Entities {
			if entity.Type != models.MessageEntityTypeBotCommand || entity.Offset != 0 || entity.Length <= 1 || entity.Length > len(m.Text) {
				continue
			}
			if m.Text[0] == '/' {
				message.Command = m.Text[1:entity.Length]
				break
			}
		}
		if err := receiver.Enqueue(message); err != nil {
			log.Printf("enqueue message failed chat=%d user=%d: %v", message.ChatID, message.UserID, err)
		}
	}
}
