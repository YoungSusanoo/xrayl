package flows

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"xrayl/internal/storage"
)

type wordsState int

const (
	wordsInitial wordsState = iota
	wordsSelecting
	wordsPageSize = 20
)

type Words struct {
	state   wordsState
	store   WordViewStore
	words   []storage.Word
	offset  int
	hasNext bool
}

func NewWords(store WordViewStore) *Words {
	return &Words{store: store}
}

func (flow *Words) Handle(ctx context.Context, message Message) Result {
	if strings.TrimSpace(message.Text) == "/cancel" {
		return Result{Texts: []string{"Просмотр завершён."}, Done: true}
	}
	switch flow.state {
	case wordsInitial:
		return flow.handleInitial(ctx, message)
	case wordsSelecting:
		return flow.handleSelection(ctx, message)
	default:
		panic("invalid words state")
	}
}

func (flow *Words) handleInitial(ctx context.Context, message Message) Result {
	return flow.showPage(ctx, message, 0)
}

func (flow *Words) handleSelection(ctx context.Context, message Message) Result {
	text := strings.TrimSpace(message.Text)
	switch text {
	case "/next":
		if !flow.hasNext {
			return Result{Texts: []string{"Это последняя страница."}}
		}
		return flow.showPage(ctx, message, flow.offset+wordsPageSize)
	case "/prev":
		if flow.offset == 0 {
			return Result{Texts: []string{"Это первая страница."}}
		}
		return flow.showPage(ctx, message, flow.offset-wordsPageSize)
	}
	number, err := strconv.Atoi(text)
	if err != nil || number < 1 || number > len(flow.words) {
		return Result{Texts: []string{fmt.Sprintf("Пришли номер от 1 до %d, /next, /prev или /cancel.", len(flow.words))}}
	}
	word := flow.words[number-1]
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	meanings, err := flow.store.ListMeanings(readCtx, message.UserID, word.ID)
	if err != nil {
		cancel()
		log.Printf("load word meanings failed for user %d: %v", message.UserID, err)
		return Result{Texts: []string{"Не удалось загрузить значения. Повтори номер или /cancel."}}
	}
	contexts, err := flow.store.ListWordContexts(readCtx, message.UserID, word.ID)
	cancel()
	if err != nil {
		log.Printf("load word contexts failed: %v", err)
		return Result{Texts: []string{"Не удалось загрузить контексты. Повтори номер или /cancel."}}
	}
	return Result{Texts: []string{formatWordMeanings(word, meanings, contexts)}}
}

func (flow *Words) showPage(ctx context.Context, message Message, offset int) Result {
	words, err := flow.store.ListWords(ctx, message.UserID, wordsPageSize+1, offset)
	if err != nil {
		log.Printf("load words failed for user %d: %v", message.UserID, err)
		return Result{Texts: []string{"Не удалось загрузить список. Повтори команду или /words, чтобы начать заново."}}
	}
	if len(words) == 0 {
		if offset == 0 {
			return Result{Texts: []string{"В словаре пока нет слов. Добавь первое через /addword."}, Done: true}
		}
		return Result{Texts: []string{"На этой странице нет слов. Открой список заново через /words."}}
	}
	flow.hasNext = len(words) > wordsPageSize
	if flow.hasNext {
		words = words[:wordsPageSize]
	}
	flow.words = words
	flow.offset = offset
	flow.state = wordsSelecting
	return Result{Texts: []string{formatWordsPage(words, offset/wordsPageSize+1, offset > 0, flow.hasNext)}}
}
