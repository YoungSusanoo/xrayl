package flows

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/dictionary"
	"xrayl/internal/storage"
)

type addwordState int

const (
	initial addwordState = iota
	inputWord
	inputMeaning
	selectSource
	inputContext
)

type Addword struct {
	state      addwordState
	handlers   map[addwordState]func(*Addword, context.Context, *tg.Bot, *models.Update) bool
	dictionary Dictionary
	store      AddwordStore
	options    []wordOption
	meaningID  int64
	saved      bool
	sourcePage sourcePage
	source     storage.Source
}

func NewAddword(dictionary Dictionary, store AddwordStore) *Addword {
	handlers := map[addwordState]func(*Addword, context.Context, *tg.Bot, *models.Update) bool{
		initial:      handleInitial,
		inputWord:    handleInputWord,
		inputMeaning: handleInputMeaning,
		selectSource: handleSelectSource,
		inputContext: handleInputContext,
	}
	return &Addword{state: initial, handlers: handlers, dictionary: dictionary, store: store}
}

func (flow *Addword) Handle(ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return false
	}
	if strings.TrimSpace(update.Message.Text) == "/cancel" {
		text := "Добавление отменено."
		if flow.saved {
			text = "Контекст не добавлен. Значение осталось в словаре."
		}
		sendText(ctx, bot, update.Message.Chat.ID, text)
		return true
	}
	return flow.handlers[flow.state](flow, ctx, bot, update)
}

func handleInitial(flow *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	if sendText(ctx, bot, update.Message.Chat.ID, "Введи английское слово или /cancel.") == nil {
		flow.state = inputWord
	}
	return false
}

func handleInputWord(flow *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	word := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID
	if word == "" || strings.HasPrefix(word, "/") {
		sendText(ctx, bot, chatID, "Введи слово текстом или /cancel.")
		return false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	entries, err := flow.dictionary.Lookup(lookupCtx, word)
	cancel()
	if err != nil {
		log.Printf("dictionary lookup failed: %v", err)
		message := "Словарь сейчас недоступен. Попробуй ввести слово ещё раз или /cancel."
		switch {
		case errors.Is(err, dictionary.ErrNotFound):
			message = "Слово не найдено. Введи другое слово или /cancel."
		case errors.Is(err, dictionary.ErrRateLimited):
			message = "Словарь ограничил запросы. Повтори ввод слова немного позже или /cancel."
		}
		sendText(ctx, bot, chatID, message)
		return false
	}
	options := makeOptions(entries)
	if len(options) == 0 {
		log.Printf("dictionary lookup returned no usable definitions")
		sendText(ctx, bot, chatID, "Словарь не вернул определений. Введи другое слово или /cancel.")
		return false
	}
	if sendText(ctx, bot, chatID, formatChoices(word, options, entries)) == nil {
		flow.options = options
		flow.state = inputMeaning
	}
	return false
}

func handleInputMeaning(flow *Addword, ctx context.Context, bot *tg.Bot, update *models.Update) bool {
	number, err := strconv.Atoi(strings.TrimSpace(update.Message.Text))
	chatID := update.Message.Chat.ID
	if err != nil || number < 1 || number > len(flow.options) {
		sendText(ctx, bot, chatID, fmt.Sprintf("Пришли номер от 1 до %d или /cancel.", len(flow.options)))
		return false
	}
	selected := flow.options[number-1]
	saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	result, err := flow.store.AddMeaning(saveCtx, update.Message.From.ID, selected.word, selected.definition.Text, selected.definition.Synonyms)
	cancel()
	if err != nil {
		log.Printf("save word meaning failed for user %d: %v", update.Message.From.ID, err)
		sendText(ctx, bot, chatID, "Не удалось сохранить значение. Повтори номер или /cancel.")
		return false
	}
	flow.saved = true
	flow.meaningID = result.MeaningID
	flow.state = selectSource
	sendText(ctx, bot, chatID, formatSaved(selected, result.MeaningCreated))
	return flow.showSources(ctx, bot, update, 0)
}
