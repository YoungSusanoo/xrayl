package flows

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/storage"
)

const sourcePageSize = 20

type sourcePage struct {
	items   []storage.Source
	offset  int
	hasNext bool
}

func (page *sourcePage) load(ctx context.Context, store SourceReader, userID int64, offset int) (bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	items, err := store.ListSources(readCtx, userID, sourcePageSize+1, offset)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}
	page.hasNext = len(items) > sourcePageSize
	if page.hasNext {
		items = items[:sourcePageSize]
	}
	page.items = items
	page.offset = offset
	return true, nil
}

func (page *sourcePage) navigation(text string) (int, string, bool) {
	switch text {
	case "/next":
		if !page.hasNext {
			return 0, "Это последняя страница.", true
		}
		return page.offset + sourcePageSize, "", true
	case "/prev":
		if page.offset == 0 {
			return 0, "Это первая страница.", true
		}
		return page.offset - sourcePageSize, "", true
	}
	return 0, "", false
}

type Sources struct {
	store  SourceReader
	page   sourcePage
	loaded bool
}

func NewSources(store SourceReader) *Sources { return &Sources{store: store} }
func (flow *Sources) Handle(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return false
	}
	text := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID
	if text == "/cancel" {
		sendText(ctx, bot, chatID, "Просмотр завершён.")
		return true
	}
	offset := 0
	if flow.loaded {
		var notice string
		var handled bool
		offset, notice, handled = flow.page.navigation(text)
		if !handled {
			sendText(ctx, bot, chatID, "Используй /next, /prev или /cancel. Новый источник — /addsource.")
			return false
		}
		if notice != "" {
			sendText(ctx, bot, chatID, notice)
			return false
		}
	}
	found, err := flow.page.load(ctx, flow.store, update.Message.From.ID, offset)
	if err != nil {
		log.Printf("load sources failed: %v", err)
		sendText(ctx, bot, chatID, "Не удалось загрузить источники. Повтори команду или /sources.")
		return false
	}
	if !found {
		if offset == 0 {
			sendText(ctx, bot, chatID, "Источников пока нет. Добавь первый через /addsource.")
			return true
		}
		sendText(ctx, bot, chatID, "На этой странице нет источников. Открой список через /sources.")
		return false
	}
	flow.loaded = true
	sendText(ctx, bot, chatID, formatSourcesPage(flow.page, false))
	return false
}

type Addsource struct {
	store   SourceWriter
	waiting bool
}

func NewAddsource(store SourceWriter) *Addsource { return &Addsource{store: store} }
func (flow *Addsource) Handle(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return false
	}
	text := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID
	if text == "/cancel" {
		sendText(ctx, bot, chatID, "Добавление источника отменено.")
		return true
	}
	if !flow.waiting {
		flow.waiting = true
		sendText(ctx, bot, chatID, "Введи название источника или /cancel.")
		return false
	}
	if text == "" || strings.HasPrefix(text, "/") {
		sendText(ctx, bot, chatID, "Введи название текстом или /cancel.")
		return false
	}
	saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	result, err := flow.store.AddSource(saveCtx, update.Message.From.ID, text)
	cancel()
	if err != nil {
		log.Printf("save source failed: %v", err)
		sendText(ctx, bot, chatID, "Не удалось сохранить источник. Повтори название или /cancel.")
		return false
	}
	status := "Источник добавлен: "
	if !result.Created {
		status = "Источник уже есть: "
	}
	sendText(ctx, bot, chatID, status+result.Source.Name)
	return true
}

func formatSourcesPage(page sourcePage, selecting bool) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Твои источники · Страница %d\n\n", page.offset/sourcePageSize+1)
	for i, source := range page.items {
		fmt.Fprintf(&text, "%d. %s\n", i+1, source.Name)
	}
	if selecting {
		text.WriteString("\nПришли номер источника.\n/skip — закончить без контекста\n")
	} else {
		text.WriteString("\n/addsource — добавить источник\n")
	}
	if page.offset > 0 {
		text.WriteString("/prev — назад\n")
	}
	if page.hasNext {
		text.WriteString("/next — дальше\n")
	}
	text.WriteString("/cancel — закончить")
	return text.String()
}
