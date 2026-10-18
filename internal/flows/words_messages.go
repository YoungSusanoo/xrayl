package flows

import (
	"fmt"
	"strings"

	"xrayl/internal/storage"
)

func formatWordsPage(words []storage.Word, page int, hasPrevious, hasNext bool) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Твои слова · Страница %d\n\n", page)
	for index, word := range words {
		fmt.Fprintf(&text, "%d. %s\n", index+1, word.Text)
	}
	text.WriteString("\nПришли номер слова.\n")
	if hasPrevious {
		text.WriteString("/prev — назад\n")
	}
	if hasNext {
		text.WriteString("/next — дальше\n")
	}
	text.WriteString("/cancel — закончить")
	return text.String()
}

func formatWordMeanings(word storage.Word, meanings []storage.Meaning, contexts []storage.WordContext) string {
	var text strings.Builder
	byMeaning := make(map[int64][]storage.WordContext)
	for _, item := range contexts {
		byMeaning[item.MeaningID] = append(byMeaning[item.MeaningID], item)
	}
	fmt.Fprintf(&text, "%s\n\n", word.Text)
	if len(meanings) == 0 {
		text.WriteString("Нет сохранённых значений.\n\n")
	}
	for index, meaning := range meanings {
		fmt.Fprintf(&text, "%d. %s\n", index+1, meaning.Definition)
		if len(meaning.Synonyms) > 0 {
			fmt.Fprintf(&text, "Синонимы: %s\n", strings.Join(meaning.Synonyms, ", "))
		}
		for _, item := range byMeaning[meaning.ID] {
			fmt.Fprintf(&text, "\nИсточник: %s\n", item.Source.Name)
			if item.Quote != "" {
				fmt.Fprintln(&text, item.Quote)
			}
			if item.Location != "" {
				fmt.Fprintf(&text, "Место: %s\n", item.Location)
			}
		}
		text.WriteString("\n")
	}
	text.WriteString("Пришли другой номер с текущей страницы или /cancel.\n/words — показать список заново.")
	return text.String()
}
