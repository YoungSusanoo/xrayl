package flows

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
)

func (flow *Addword) showSources(ctx context.Context, message Message, offset int) Result {
	found, err := flow.sourcePage.load(ctx, flow.store, message.UserID, offset)
	if err != nil {
		log.Printf("load sources for saved meaning failed: %v", err)
		return Result{Texts: []string{"Значение сохранено. Не удалось загрузить источники. Пришли /retry, чтобы повторить, или /skip, чтобы закончить без контекста."}}
	}
	if !found {
		if offset == 0 {
			return Result{Texts: []string{"Значение сохранено. Источников пока нет. Добавь источник через /addsource."}, Done: true}
		}
		return Result{Texts: []string{"На этой странице нет источников. Повтори переход или /skip."}}
	}
	return Result{Texts: []string{formatSourcesPage(flow.sourcePage, true)}}
}

func finishWithoutContext() Result {
	return Result{Texts: []string{"Контекст не добавлен. Значение осталось в словаре."}, Done: true}
}

func (flow *Addword) handleSelectSource(ctx context.Context, message Message) Result {
	text := strings.TrimSpace(message.Text)
	if text == "/skip" {
		return finishWithoutContext()
	}
	if len(flow.sourcePage.items) == 0 {
		return flow.showSources(ctx, message, 0)
	}
	if text == "/retry" {
		return flow.showSources(ctx, message, flow.sourcePage.offset)
	}
	if offset, notice, handled := flow.sourcePage.navigation(text); handled {
		if notice != "" {
			return Result{Texts: []string{notice}}
		}
		return flow.showSources(ctx, message, offset)
	}
	number, err := strconv.Atoi(text)
	if err != nil || number < 1 || number > len(flow.sourcePage.items) {
		return Result{Texts: []string{fmt.Sprintf("Пришли номер источника от 1 до %d или /skip.", len(flow.sourcePage.items))}}
	}
	flow.source = flow.sourcePage.items[number-1]
	flow.state = inputContext
	return Result{Texts: []string{"Источник: " + flow.source.Name + "\nПришли контекст одним сообщением. При желании добавь главу, страницу или таймкод.\n/skip — закончить без контекста, /cancel — отменить ввод контекста."}}
}

func (flow *Addword) handleInputContext(ctx context.Context, message Message) Result {
	text := strings.TrimSpace(message.Text)
	if text == "/skip" {
		return finishWithoutContext()
	}
	if text == "" || strings.HasPrefix(text, "/") {
		return Result{Texts: []string{"Пришли контекст текстом или /skip, чтобы закончить без контекста."}}
	}
	_, err := flow.store.AddContext(ctx, message.UserID, flow.meaningID, flow.source.ID, text)
	if err != nil {
		log.Printf("save context failed: %v", err)
		return Result{Texts: []string{"Не удалось сохранить контекст. Пришли контекст ещё раз, /skip или /cancel. Значение осталось в словаре."}}
	}
	return Result{Texts: []string{"Контекст сохранён.\n\nИсточник: " + flow.source.Name + "\n" + text}, Done: true}
}
