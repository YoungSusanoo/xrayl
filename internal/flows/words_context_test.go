package flows_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type contextViewStore struct {
	readStore
	contexts func(context.Context, int64, int64) ([]storage.WordContext, error)
}

func (s contextViewStore) ListWordContexts(ctx context.Context, user, word int64) ([]storage.WordContext, error) {
	return s.contexts(ctx, user, word)
}

func TestWordsDisplaysContextsUnderTheirMeanings(t *testing.T) {
	b, sent := telegram(t)
	calls := 0
	f := flows.NewWords(contextViewStore{readStore: readStore{words: func(context.Context, int64, int, int) ([]storage.Word, error) {
		return []storage.Word{{ID: 10, Text: "book"}}, nil
	}, meanings: func(context.Context, int64, int64) ([]storage.Meaning, error) {
		return []storage.Meaning{{ID: 1, Definition: "Printed work."}, {ID: 2, Definition: "To reserve."}}, nil
	}}, contexts: func(ctx context.Context, user, word int64) ([]storage.WordContext, error) {
		checkReadContext(t, ctx)
		calls++
		if user != 42 || word != 10 {
			t.Error("wrong context lookup")
		}
		if calls == 1 {
			return nil, errors.New("unavailable")
		}
		return []storage.WordContext{{ID: 1, MeaningID: 2, Source: storage.Source{ID: 9, Name: "Dune"}, Quote: "Book a room.\npage 2"}, {ID: 2, MeaningID: 1, Source: storage.Source{Name: "Article"}, Quote: "Read a book."}}, nil
	}})
	f.Handle(context.Background(), b, update("/words"))
	f.Handle(context.Background(), b, update("1"))
	if strings.Contains(strings.Join(sent.texts(), "\n"), "Printed work.") {
		t.Fatal("context read failure produced incomplete card")
	}
	f.Handle(context.Background(), b, update("1"))
	card := sent.texts()[len(sent.texts())-1]
	if !strings.Contains(card, "Источник: Dune") || !strings.Contains(card, "page 2") || strings.Index(card, "Read a book.") > strings.Index(card, "2. To reserve.") || strings.Index(card, "Book a room.") < strings.Index(card, "2. To reserve.") {
		t.Fatalf("contexts grouped incorrectly: %s", card)
	}
	if calls != 2 {
		t.Fatal("context retry failed")
	}
}
