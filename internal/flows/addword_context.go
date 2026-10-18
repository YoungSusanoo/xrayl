package flows

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (flow *Addword) showSources(ctx context.Context, bot *tg.Bot, update *models.Update, offset int) bool {
	chatID := update.Message.Chat.ID
	found, err := flow.sourcePage.load(ctx, flow.store, update.Message.From.ID, offset)
	if err != nil {
		log.Printf("load sources for saved meaning failed: %v", err)
		sendText(ctx, bot, chatID, "Значение сохранено. Не удалось загрузить источники. Пришли /retry, чтобы повторить, или /skip, чтобы закончить без контекста.")
		return false
	}
	if !found {
		if offset == 0 {
			sendText(ctx, bot, chatID, "Значение сохранено. Источников пока нет. Добавь источник через /addsource.")
			return true
		}
		sendText(ctx, bot, chatID, "На этой странице нет источников. Повтори переход или /skip.")
		return false
	}
	sendText(ctx, bot, chatID, formatSourcesPage(flow.sourcePage, true))
	return false
}

func finishWithoutContext(ctx context.Context, bot *tg.Bot, chatID int64) bool {
	sendText(ctx, bot, chatID, "Контекст не добавлен. Значение осталось в словаре.")
	return true
}

func handleSelectSource(flow *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	text := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID
	if text == "/skip" {
		return finishWithoutContext(ctx, bot, chatID)
	}
	if len(flow.sourcePage.items) == 0 {
		return flow.showSources(ctx, bot, update, 0)
	}
	if text == "/retry" {
		return flow.showSources(ctx, bot, update, flow.sourcePage.offset)
	}
	if offset, notice, handled := flow.sourcePage.navigation(text); handled {
		if notice != "" {
			sendText(ctx, bot, chatID, notice)
			return false
		}
		return flow.showSources(ctx, bot, update, offset)
	}
	number, err := strconv.Atoi(text)
	if err != nil || number < 1 || number > len(flow.sourcePage.items) {
		sendText(ctx, bot, chatID, fmt.Sprintf("Пришли номер источника от 1 до %d или /skip.", len(flow.sourcePage.items)))
		return false
	}
	flow.source = flow.sourcePage.items[number-1]
	flow.state = inputContext
	sendText(ctx, bot, chatID, "Источник: "+flow.source.Name+"\nПришли контекст одним сообщением. При желании добавь главу, страницу или таймкод.\n/skip — закончить без контекста, /cancel — отменить ввод контекста.")
	return false
}

func handleInputContext(flow *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	text := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID
	if text == "/skip" {
		return finishWithoutContext(ctx, bot, chatID)
	}
	if text == "" || strings.HasPrefix(text, "/") {
		sendText(ctx, bot, chatID, "Пришли контекст текстом или /skip, чтобы закончить без контекста.")
		return false
	}
	saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err := flow.store.AddContext(saveCtx, update.Message.From.ID, flow.meaningID, flow.source.ID, text)
	cancel()
	if err != nil {
		log.Printf("save context failed: %v", err)
		sendText(ctx, bot, chatID, "Не удалось сохранить контекст. Пришли контекст ещё раз, /skip или /cancel. Значение осталось в словаре.")
		return false
	}
	sendText(ctx, bot, chatID, "Контекст сохранён.\n\nИсточник: "+flow.source.Name+"\n"+text)
	return true
}
