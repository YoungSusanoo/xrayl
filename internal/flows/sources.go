package flows

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xrayl/internal/storage"
)

const sourcePageSize = 20

type sourcePage struct {
	items   []storage.Source
	offset  int
	hasNext bool
}

func (page *sourcePage) load(ctx context.Context, store SourceReader, userID int64, offset int) (bool, error) {
	items, err := store.ListSources(ctx, userID, sourcePageSize+1, offset)
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
func (flow *Sources) Handle(ctx context.Context, message Message) Result {
	text := strings.TrimSpace(message.Text)
	if text == "/cancel" {
		return Result{Texts: []string{"Просмотр завершён."}, Done: true}
	}
	offset := 0
	if flow.loaded {
		var notice string
		var handled bool
		offset, notice, handled = flow.page.navigation(text)
		if !handled {
			return Result{Texts: []string{"Используй /next, /prev или /cancel. Новый источник — /addsource."}}
		}
		if notice != "" {
			return Result{Texts: []string{notice}}
		}
	}
	found, err := flow.page.load(ctx, flow.store, message.UserID, offset)
	if err != nil {
		log.Printf("load sources failed: %v", err)
		return Result{Texts: []string{"Не удалось загрузить источники. Повтори команду или /sources."}}
	}
	if !found {
		if offset == 0 {
			return Result{Texts: []string{"Источников пока нет. Добавь первый через /addsource."}, Done: true}
		}
		return Result{Texts: []string{"На этой странице нет источников. Открой список через /sources."}}
	}
	flow.loaded = true
	return Result{Texts: []string{formatSourcesPage(flow.page, false)}}
}

type Addsource struct {
	store   SourceWriter
	waiting bool
}

func NewAddsource(store SourceWriter) *Addsource { return &Addsource{store: store} }
func (flow *Addsource) Handle(ctx context.Context, message Message) Result {
	text := strings.TrimSpace(message.Text)
	if text == "/cancel" {
		return Result{Texts: []string{"Добавление источника отменено."}, Done: true}
	}
	if !flow.waiting {
		flow.waiting = true
		return Result{Texts: []string{"Введи название источника или /cancel."}}
	}
	if text == "" || strings.HasPrefix(text, "/") {
		return Result{Texts: []string{"Введи название текстом или /cancel."}}
	}
	saved, err := flow.store.AddSource(ctx, message.UserID, text)
	if err != nil {
		log.Printf("save source failed: %v", err)
		return Result{Texts: []string{"Не удалось сохранить источник. Повтори название или /cancel."}}
	}
	status := "Источник добавлен: "
	if !saved.Created {
		status = "Источник уже есть: "
	}
	return Result{Texts: []string{status + saved.Source.Name}, Done: true}
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
