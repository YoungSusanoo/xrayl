package flows_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"xrayl/internal/dictionary"
	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type contextStore struct {
	saveFunc
	sourceReaderFunc
	context func(context.Context, int64, int64, int64, string, string) (int64, error)
}

func (s contextStore) ListSources(ctx context.Context, user int64, limit, offset int) ([]storage.Source, error) {
	return s.sourceReaderFunc(ctx, user, limit, offset)
}
func (s contextStore) AddContext(ctx context.Context, user, meaning, source int64, quote, location string) (int64, error) {
	return s.context(ctx, user, meaning, source, quote, location)
}

func TestAddwordContextSavesAndRetries(t *testing.T) {
	b, sent := telegram(t)
	saves, lists, contexts, lookups := 0, 0, 0, 0
	all := sourceFixture(21)
	var saved []string
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { lookups++; return entries(), nil }), contextStore{
		saveFunc: func(ctx context.Context, user int64, word, definition string, synonyms []string) (storage.AddMeaningResult, error) {
			saves++
			return storage.AddMeaningResult{WordID: 1, MeaningID: 7, MeaningCreated: true}, nil
		},
		sourceReaderFunc: func(ctx context.Context, user int64, limit, offset int) ([]storage.Source, error) {
			checkReadContext(t, ctx)
			lists++
			if lists == 1 || lists == 3 {
				return nil, errors.New("unavailable")
			}
			if user != 42 || limit != 21 {
				t.Error("wrong source query")
			}
			return all[offset:min(offset+limit, len(all))], nil
		},
		context: func(ctx context.Context, user, meaning, source int64, quote, location string) (int64, error) {
			checkReadContext(t, ctx)
			contexts++
			if user != 42 || meaning != 7 || source != 120 {
				t.Errorf("wrong context IDs: %d %d %d", user, meaning, source)
			}
			saved = append(saved, quote+"|"+location)
			if contexts == 1 {
				return 0, errors.New("unavailable")
			}
			return 99, nil
		},
	})
	for _, text := range []string{"/addword", "book", "1", "1", "/next", "/next", "1", " ", "/unknown", " The book was open. ", " page 2 "} {
		if f.Handle(context.Background(), b, update(text)) {
			t.Fatalf("flow completed on %q", text)
		}
	}
	sent.failNext()
	if !f.Handle(context.Background(), b, update("page 2")) {
		t.Fatal("successful context save did not finish")
	}
	if saves != 1 || lookups != 1 || lists != 4 || contexts != 2 || !reflect.DeepEqual(saved, []string{"The book was open.|page 2", "The book was open.|page 2"}) {
		t.Fatalf("counts: saves=%d lookups=%d lists=%d contexts=%d saved=%v", saves, lookups, lists, contexts, saved)
	}
	output := strings.Join(sent.texts(), "\n")
	for _, want := range []string{"source20", "The book was open.", "page 2", "Контекст сохранён"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestAddwordContextSkipAndCancel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inputs    []string
		wantSaves int
		location  string
	}{
		{"skip source", []string{"/skip"}, 0, ""},
		{"cancel source", []string{"/cancel"}, 0, ""},
		{"skip quote", []string{"1", "/skip"}, 0, ""},
		{"cancel quote", []string{"1", "/cancel"}, 0, ""},
		{"skip location", []string{"1", "A book.", "/skip"}, 1, ""},
		{"cancel location", []string{"1", "A book.", "/cancel"}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, sent := telegram(t)
			meaningSaves, contextSaves := 0, 0
			f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { return entries(), nil }), contextStore{
				saveFunc: func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
					meaningSaves++
					return storage.AddMeaningResult{MeaningID: 7}, nil
				},
				sourceReaderFunc: func(context.Context, int64, int, int) ([]storage.Source, error) { return sourceFixture(1), nil },
				context: func(_ context.Context, _, _, _ int64, quote, location string) (int64, error) {
					contextSaves++
					if quote != "A book." || location != tc.location {
						t.Errorf("context: %q %q", quote, location)
					}
					return 1, nil
				},
			})
			for _, text := range []string{"/addword", "book", "1"} {
				if f.Handle(context.Background(), b, update(text)) {
					t.Fatal("flow ended before context")
				}
			}
			for i, text := range tc.inputs {
				if got := f.Handle(context.Background(), b, update(text)); got != (i == len(tc.inputs)-1) {
					t.Fatalf("unexpected completion on %s", text)
				}
			}
			if meaningSaves != 1 || contextSaves != tc.wantSaves {
				t.Fatalf("save counts: %d %d", meaningSaves, contextSaves)
			}
			if tc.wantSaves == 0 && !strings.Contains(strings.Join(sent.texts(), "\n"), "Значение осталось в словаре") {
				t.Fatal("cancel/skip did not explain saved meaning")
			}
		})
	}
}

func TestAddwordContextSendFailureKeepsSelectedSource(t *testing.T) {
	b, sent := telegram(t)
	contexts := 0
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { return entries(), nil }), contextStore{
		saveFunc: func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
			return storage.AddMeaningResult{MeaningID: 7}, nil
		},
		sourceReaderFunc: func(context.Context, int64, int, int) ([]storage.Source, error) { return sourceFixture(1), nil },
		context: func(_ context.Context, _, _, source int64, quote, location string) (int64, error) {
			contexts++
			if source != 100 || quote != "Quote." {
				t.Error("source or quote lost")
			}
			return 1, nil
		},
	})
	f.Handle(context.Background(), b, update("/addword"))
	f.Handle(context.Background(), b, update("book"))
	sent.failNext()
	f.Handle(context.Background(), b, update("1"))
	sent.failNext()
	f.Handle(context.Background(), b, update("1"))
	sent.failNext()
	f.Handle(context.Background(), b, update("Quote."))
	if !f.Handle(context.Background(), b, update("/skip")) || contexts != 1 {
		t.Fatal("failed send lost context draft")
	}
}
