package freedictionaryapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"xrayl/internal/dictionary"
)

type response struct {
	Word    string          `json:"word"`
	Entries []entryResponse `json:"entries"`
	Source  struct {
		URL     string             `json:"url"`
		License dictionary.License `json:"license"`
	} `json:"source"`
}

type entryResponse struct {
	Language struct {
		Code string `json:"code"`
	} `json:"language"`
	PartOfSpeech string          `json:"partOfSpeech"`
	Senses       []senseResponse `json:"senses"`
	Synonyms     []string        `json:"synonyms"`
	Antonyms     []string        `json:"antonyms"`
}

type senseResponse struct {
	Definition string          `json:"definition"`
	Examples   []string        `json:"examples"`
	Synonyms   []string        `json:"synonyms"`
	Antonyms   []string        `json:"antonyms"`
	Subsenses  []senseResponse `json:"subsenses"`
}

func decodeEntries(reader io.Reader) ([]dictionary.Entry, error) {
	const maxResponseBytes = 2 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read dictionary response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, dictionary.ErrInvalidResponse
	}
	var decoded response
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", dictionary.ErrInvalidResponse, err)
	}
	if strings.TrimSpace(decoded.Word) == "" || decoded.Entries == nil {
		return nil, dictionary.ErrInvalidResponse
	}
	if len(decoded.Entries) == 0 {
		return nil, dictionary.ErrNotFound
	}
	sources := []string{}
	if strings.TrimSpace(decoded.Source.URL) != "" {
		sources = append(sources, decoded.Source.URL)
	}
	if decoded.Source.URL != "https://freedictionaryapi.com" {
		sources = append(sources, "https://freedictionaryapi.com")
	}
	var entries []dictionary.Entry
	for _, entry := range decoded.Entries {
		if entry.Language.Code == "" {
			return nil, dictionary.ErrInvalidResponse
		}
		if entry.Language.Code != "en" && entry.Language.Code != "eng" {
			continue
		}
		meaning := dictionary.Meaning{
			PartOfSpeech: entry.PartOfSpeech,
			Synonyms:     entry.Synonyms,
			Antonyms:     entry.Antonyms,
		}
		meaning.Definitions = appendSenses(meaning.Definitions, entry.Senses)
		if len(meaning.Definitions) == 0 {
			return nil, dictionary.ErrInvalidResponse
		}
		entries = append(entries, dictionary.Entry{
			Word: decoded.Word, Meanings: []dictionary.Meaning{meaning},
			SourceURLs: sources, License: decoded.Source.License,
		})
	}
	if len(entries) == 0 {
		return nil, dictionary.ErrInvalidResponse
	}
	return entries, nil
}

func appendSenses(definitions []dictionary.Definition, senses []senseResponse) []dictionary.Definition {
	for _, sense := range senses {
		if strings.TrimSpace(sense.Definition) != "" {
			definitions = append(definitions, dictionary.Definition{
				Text: sense.Definition, Example: strings.Join(sense.Examples, "\n"),
				Synonyms: sense.Synonyms, Antonyms: sense.Antonyms,
			})
		}
		definitions = appendSenses(definitions, sense.Subsenses)
	}
	return definitions
}
