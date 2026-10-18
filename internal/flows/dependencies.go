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

type WordReader interface {
	ListWords(ctx context.Context, userID int64, limit, offset int) ([]storage.Word, error)
	ListMeanings(ctx context.Context, userID, wordID int64) ([]storage.Meaning, error)
}

type Store interface {
	WordStore
	WordReader
	SourceWriter
	SourceReader
	ContextWriter
	ContextReader
}

type SourceWriter interface {
	AddSource(context.Context, int64, string) (storage.AddSourceResult, error)
}

type SourceReader interface {
	ListSources(ctx context.Context, userID int64, limit, offset int) ([]storage.Source, error)
}

type ContextWriter interface {
	AddContext(ctx context.Context, userID, meaningID, sourceID int64, quote, location string) (int64, error)
}

type ContextReader interface {
	ListWordContexts(ctx context.Context, userID, wordID int64) ([]storage.WordContext, error)
}

type AddwordStore interface {
	WordStore
	SourceReader
	ContextWriter
}

type WordViewStore interface {
	WordReader
	ContextReader
}
