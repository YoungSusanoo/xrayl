package flows

import (
	"context"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type addwordState int

const (
	initial addwordState = iota
	inputWord
)

type Addword struct {
	state    addwordState
	handlers map[addwordState]func(*Addword, context.Context, *tg.Bot, *models.Update) bool
}

func NewAddword() *Addword {
	handlers := map[addwordState]func(*Addword, context.Context, *tg.Bot, *models.Update) bool{
		initial:   handleInitial,
		inputWord: handleInputWord,
	}
	return &Addword{initial, handlers}
}

func (flow *Addword) Handle(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	return flow.handlers[flow.state](flow, ctx, bot, update)
}

func handleInitial(flow *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	bot.SendMessage(ctx, &tg.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Введи слово, черт",
	})
	flow.state = inputWord
	return false
}

func handleInputWord(_ *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	bot.SendMessage(ctx, &tg.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Ну типо ввел",
	})
	return true
}
