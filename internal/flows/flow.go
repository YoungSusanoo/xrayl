package flows

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type Flow interface {
	Handle(context.Context, *bot.Bot, *models.Update) bool
}
