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

	"xrayl/internal/storage"
)

type wordsState int

const (
	wordsInitial wordsState = iota
	wordsSelecting
	wordsPageSize = 20
)

type Words struct {
	state    wordsState
	handlers map[wordsState]func(context.Context, *tg.Bot, *models.Update) bool
	store    WordViewStore
	words    []storage.Word
	offset   int
	hasNext  bool
}

func NewWords(store WordViewStore) *Words {
	flow := &Words{store: store}
	flow.handlers = map[wordsState]func(context.Context, *tg.Bot, *models.Update) bool{
		wordsInitial:   flow.handleInitial,
		wordsSelecting: flow.handleSelection,
	}
	return flow
}

func (flow *Words) Handle(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return false
	}
	if strings.TrimSpace(update.Message.Text) == "/cancel" {
		sendText(ctx, bot, update.Message.Chat.ID, "Просмотр завершён.")
		return true
	}
	return flow.handlers[flow.state](ctx, bot, update)
}

func (flow *Words) handleInitial(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	return flow.showPage(ctx, bot, update, 0)
}

func (flow *Words) handleSelection(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	text := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID
	switch text {
	case "/next":
		if !flow.hasNext {
			sendText(ctx, bot, chatID, "Это последняя страница.")
			return false
		}
		return flow.showPage(ctx, bot, update, flow.offset+wordsPageSize)
	case "/prev":
		if flow.offset == 0 {
			sendText(ctx, bot, chatID, "Это первая страница.")
			return false
		}
		return flow.showPage(ctx, bot, update, flow.offset-wordsPageSize)
	}
	number, err := strconv.Atoi(text)
	if err != nil || number < 1 || number > len(flow.words) {
		sendText(ctx, bot, chatID, fmt.Sprintf("Пришли номер от 1 до %d, /next, /prev или /cancel.", len(flow.words)))
		return false
	}
	word := flow.words[number-1]
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	meanings, err := flow.store.ListMeanings(readCtx, update.Message.From.ID, word.ID)
	if err != nil {
		cancel()
		log.Printf("load word meanings failed for user %d: %v", update.Message.From.ID, err)
		sendText(ctx, bot, chatID, "Не удалось загрузить значения. Повтори номер или /cancel.")
		return false
	}
	contexts, err := flow.store.ListWordContexts(readCtx, update.Message.From.ID, word.ID)
	cancel()
	if err != nil {
		log.Printf("load word contexts failed: %v", err)
		sendText(ctx, bot, chatID, "Не удалось загрузить контексты. Повтори номер или /cancel.")
		return false
	}
	sendText(ctx, bot, chatID, formatWordMeanings(word, meanings, contexts))
	return false
}

func (flow *Words) showPage(ctx context.Context, bot *tg.Bot, update *models.Update, offset int) bool {
	chatID := update.Message.Chat.ID
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	words, err := flow.store.ListWords(readCtx, update.Message.From.ID, wordsPageSize+1, offset)
	cancel()
	if err != nil {
		log.Printf("load words failed for user %d: %v", update.Message.From.ID, err)
		sendText(ctx, bot, chatID, "Не удалось загрузить список. Повтори команду или /words, чтобы начать заново.")
		return false
	}
	if len(words) == 0 {
		if offset == 0 {
			sendText(ctx, bot, chatID, "В словаре пока нет слов. Добавь первое через /addword.")
			return true
		}
		sendText(ctx, bot, chatID, "На этой странице нет слов. Открой список заново через /words.")
		return false
	}
	flow.hasNext = len(words) > wordsPageSize
	if flow.hasNext {
		words = words[:wordsPageSize]
	}
	flow.words = words
	flow.offset = offset
	flow.state = wordsSelecting
	sendText(ctx, bot, chatID, formatWordsPage(words, offset/wordsPageSize+1, offset > 0, flow.hasNext))
	return false
}
