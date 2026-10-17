package flows

import (
	"context"

	"xrayl/internal/dictionary"
	"xrayl/internal/storage"
)

type Dictionary interface {
	Lookup(context.Context, string) ([]dictionary.Entry, error)
}

type WordStore interface {
	AddMeaning(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error)
}
