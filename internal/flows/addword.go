package flows

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"xrayl/internal/dictionary"
	"xrayl/internal/storage"
)

type addwordState int

type wordOption struct {
	word         string
	partOfSpeech string
	definition   dictionary.Definition
	sourceURLs   []string
	license      dictionary.License
}

const (
	initial addwordState = iota
	inputWord
	inputMeaning
	selectSource
	inputContext
)

type Addword struct {
	state      addwordState
	dictionary Dictionary
	store      AddwordStore
	options    []wordOption
	meaningID  int64
	saved      bool
	sourcePage sourcePage
	source     storage.Source
}

func NewAddword(dictionary Dictionary, store AddwordStore) *Addword {
	return &Addword{dictionary: dictionary, store: store}
}

func (flow *Addword) Handle(ctx context.Context, message Message) Result {
	if strings.TrimSpace(message.Text) == "/cancel" {
		text := "Добавление отменено."
		if flow.saved {
			text = "Контекст не добавлен. Значение осталось в словаре."
		}
		return Result{Texts: []string{text}, Done: true}
	}
	switch flow.state {
	case initial:
		return flow.handleInitial(ctx, message)
	case inputWord:
		return flow.handleInputWord(ctx, message)
	case inputMeaning:
		return flow.handleInputMeaning(ctx, message)
	case selectSource:
		return flow.handleSelectSource(ctx, message)
	case inputContext:
		return flow.handleInputContext(ctx, message)
	default:
		panic("invalid addword state")
	}
}

func (flow *Addword) handleInitial(ctx context.Context, message Message) Result {
	flow.state = inputWord
	return Result{Texts: []string{"Введи английское слово или /cancel."}}
}

func (flow *Addword) handleInputWord(ctx context.Context, message Message) Result {
	word := strings.TrimSpace(message.Text)
	if word == "" || strings.HasPrefix(word, "/") {
		return Result{Texts: []string{"Введи слово текстом или /cancel."}}
	}
	entries, err := flow.dictionary.Lookup(ctx, word)
	if err != nil {
		log.Printf("dictionary lookup failed: %v", err)
		notice := "Словарь сейчас недоступен. Попробуй ввести слово ещё раз или /cancel."
		switch {
		case errors.Is(err, dictionary.ErrNotFound):
			notice = "Слово не найдено. Введи другое слово или /cancel."
		case errors.Is(err, dictionary.ErrRateLimited):
			notice = "Словарь ограничил запросы. Повтори ввод слова немного позже или /cancel."
		}
		return Result{Texts: []string{notice}}
	}
	options := makeOptions(entries)
	if len(options) == 0 {
		log.Printf("dictionary lookup returned no usable definitions")
		return Result{Texts: []string{"Словарь не вернул определений. Введи другое слово или /cancel."}}
	}
	flow.options = options
	flow.state = inputMeaning
	return Result{Texts: []string{formatChoices(word, options, entries)}}
}

func (flow *Addword) handleInputMeaning(ctx context.Context, message Message) Result {
	number, err := strconv.Atoi(strings.TrimSpace(message.Text))
	if err != nil || number < 1 || number > len(flow.options) {
		return Result{Texts: []string{fmt.Sprintf("Пришли номер от 1 до %d или /cancel.", len(flow.options))}}
	}
	selected := flow.options[number-1]
	saved, err := flow.store.AddMeaning(ctx, message.UserID, selected.word, selected.definition.Text, selected.definition.Synonyms)
	if err != nil {
		log.Printf("save word meaning failed for user %d: %v", message.UserID, err)
		return Result{Texts: []string{"Не удалось сохранить значение. Повтори номер или /cancel."}}
	}
	flow.saved = true
	flow.meaningID = saved.MeaningID
	flow.state = selectSource
	response := flow.showSources(ctx, message, 0)
	response.Texts = append([]string{formatSaved(selected, saved.MeaningCreated)}, response.Texts...)
	return response
}

func makeOptions(entries []dictionary.Entry) []wordOption {
	var options []wordOption
	for _, entry := range entries {
		if strings.TrimSpace(entry.Word) == "" {
			continue
		}
		for _, meaning := range entry.Meanings {
			for _, definition := range meaning.Definitions {
				if strings.TrimSpace(definition.Text) == "" {
					continue
				}
				options = append(options, wordOption{word: entry.Word, partOfSpeech: meaning.PartOfSpeech, definition: definition, sourceURLs: entry.SourceURLs, license: entry.License})
			}
		}
	}
	return options
}

func formatChoices(word string, options []wordOption, entries []dictionary.Entry) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n", word)
	for index, option := range options {
		fmt.Fprintf(&text, "%d. %s (%s) — %s\n\n", index+1, option.word, option.partOfSpeech, option.definition.Text)
	}
	text.WriteString("Пришли номер значения или /cancel.\n")
	seen := make(map[string]bool)
	for _, entry := range entries {
		source := formatSource(entry.SourceURLs, entry.License)
		if source != "" && !seen[source] {
			text.WriteString("\n" + source)
			seen[source] = true
		}
	}
	return text.String()
}

func formatSaved(option wordOption, created bool) string {
	status := "Слово и значение сохранены."
	if !created {
		status = "Значение уже есть. Синонимы обновлены."
	}
	text := fmt.Sprintf("%s\n\n%s (%s)\n%s", status, option.word, option.partOfSpeech, option.definition.Text)
	if len(option.definition.Synonyms) > 0 {
		text += "\n\nСинонимы: " + strings.Join(option.definition.Synonyms, ", ")
	}
	if source := formatSource(option.sourceURLs, option.license); source != "" {
		text += "\n\n" + source
	}
	return text
}

func formatSource(urls []string, license dictionary.License) string {
	var parts []string
	for _, url := range urls {
		if strings.TrimSpace(url) != "" {
			parts = append(parts, "Источник: "+url)
		}
	}
	if license.Name != "" || license.URL != "" {
		parts = append(parts, strings.TrimSpace(license.Name+" "+license.URL))
	}
	return strings.Join(parts, "\n")
}
