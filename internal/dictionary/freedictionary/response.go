package freedictionary

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"xrayl/internal/dictionary"
)

const maxResponseBytes = 2 << 20

type entryResponse struct {
	Word       string            `json:"word"`
	Meanings   []meaningResponse `json:"meanings"`
	SourceURLs []string          `json:"sourceUrls"`
	License    licenseResponse   `json:"license"`
}

type meaningResponse struct {
	PartOfSpeech string               `json:"partOfSpeech"`
	Definitions  []definitionResponse `json:"definitions"`
	Synonyms     []string             `json:"synonyms"`
	Antonyms     []string             `json:"antonyms"`
}

type definitionResponse struct {
	Text     string   `json:"definition"`
	Example  string   `json:"example"`
	Synonyms []string `json:"synonyms"`
	Antonyms []string `json:"antonyms"`
}

type licenseResponse struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func decodeEntries(body io.Reader) ([]dictionary.Entry, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read dictionary response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeds 2 MiB", dictionary.ErrInvalidResponse)
	}

	var response []entryResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %w", dictionary.ErrInvalidResponse, err)
	}
	if len(response) == 0 {
		return nil, fmt.Errorf("%w: no entries", dictionary.ErrInvalidResponse)
	}

	entries := make([]dictionary.Entry, 0, len(response))
	for _, source := range response {
		if strings.TrimSpace(source.Word) == "" {
			return nil, fmt.Errorf("%w: entry has no word", dictionary.ErrInvalidResponse)
		}
		entry := dictionary.Entry{
			Word:       source.Word,
			Meanings:   make([]dictionary.Meaning, 0, len(source.Meanings)),
			SourceURLs: source.SourceURLs,
			License:    dictionary.License{Name: source.License.Name, URL: source.License.URL},
		}
		definitionCount := 0
		for _, sourceMeaning := range source.Meanings {
			meaning := dictionary.Meaning{
				PartOfSpeech: sourceMeaning.PartOfSpeech,
				Definitions:  make([]dictionary.Definition, 0, len(sourceMeaning.Definitions)),
				Synonyms:     sourceMeaning.Synonyms,
				Antonyms:     sourceMeaning.Antonyms,
			}
			for _, definition := range sourceMeaning.Definitions {
				if strings.TrimSpace(definition.Text) == "" {
					return nil, fmt.Errorf("%w: blank definition", dictionary.ErrInvalidResponse)
				}
				meaning.Definitions = append(meaning.Definitions, dictionary.Definition{
					Text: definition.Text, Example: definition.Example,
					Synonyms: definition.Synonyms, Antonyms: definition.Antonyms,
				})
				definitionCount++
			}
			entry.Meanings = append(entry.Meanings, meaning)
		}
		if definitionCount == 0 {
			return nil, fmt.Errorf("%w: entry has no definitions", dictionary.ErrInvalidResponse)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
