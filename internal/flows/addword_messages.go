package flows

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf16"

	tg "github.com/go-telegram/bot"

	"xrayl/internal/dictionary"
)

type wordOption struct {
	word         string
	partOfSpeech string
	definition   dictionary.Definition
	sourceURLs   []string
	license      dictionary.License
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

func sendText(ctx context.Context, bot *tg.Bot, chatID int64, text string) error {
	for _, chunk := range splitText(text) {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := bot.SendMessage(sendCtx, &tg.SendMessageParams{ChatID: chatID, Text: chunk})
		cancel()
		if err != nil {
			log.Printf("send addword message to chat %d failed: %v", chatID, err)
			return err
		}
	}
	return nil
}

func splitText(text string) []string {
	var chunks []string
	start, units := 0, 0
	for index, r := range text {
		length := utf16.RuneLen(r)
		if units+length > 3000 {
			chunks = append(chunks, text[start:index])
			start, units = index, 0
		}
		units += length
	}
	if start < len(text) {
		chunks = append(chunks, text[start:])
	}
	return chunks
}
