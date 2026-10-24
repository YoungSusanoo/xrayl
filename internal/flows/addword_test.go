package flows_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"xrayl/internal/dictionary"
	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type lookupFunc func(context.Context, string) ([]dictionary.Entry, error)

func (f lookupFunc) Lookup(ctx context.Context, word string) ([]dictionary.Entry, error) {
	return f(ctx, word)
}

type saveFunc func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error)

func (f saveFunc) ListSources(context.Context, int64, int, int) ([]storage.Source, error) {
	return nil, nil
}
func (f saveFunc) AddContext(context.Context, int64, int64, int64, string) (int64, error) {
	return 0, errors.New("unexpected context save")
}

func (f saveFunc) AddMeaning(ctx context.Context, userID int64, word, definition string, synonyms []string) (storage.AddMeaningResult, error) {
	return f(ctx, userID, word, definition, synonyms)
}

type flowLog struct{ messages []string }

func outputs(t *testing.T) (*flowLog, *flowLog) {
	t.Helper()
	log := &flowLog{}
	return log, log
}

func (r *flowLog) texts() []string { return r.messages }

func update(text string) flows.Message {
	return flows.Message{ChatID: 10, UserID: 42, Text: text}
}

func handle(f flows.Flow, log *flowLog, message flows.Message) bool {
	result := f.Handle(context.Background(), message)
	log.messages = append(log.messages, result.Texts...)
	return result.Done
}

func entries() []dictionary.Entry {
	return []dictionary.Entry{
		{Word: "book", Meanings: []dictionary.Meaning{
			{PartOfSpeech: "noun", Synonyms: []string{"group synonym"}, Definitions: []dictionary.Definition{
				{Text: "A written work.", Synonyms: []string{"volume"}}, {Text: "An account record."},
			}},
			{PartOfSpeech: "verb", Definitions: []dictionary.Definition{{Text: "To reserve.", Synonyms: []string{"hold"}}}},
		}},
		{Word: "bake", Meanings: []dictionary.Meaning{
			{PartOfSpeech: "verb", Synonyms: []string{"group bake"}, Definitions: []dictionary.Definition{{Text: "To cook in an oven.", Synonyms: []string{"cook"}}}},
		}, SourceURLs: []string{"https://en.wiktionary.org/wiki/bake"}, License: dictionary.License{Name: "CC BY-SA 3.0", URL: "https://creativecommons.org/licenses/by-sa/3.0/"}},
	}
}

func TestAddwordSelectsAndSavesChosenDefinition(t *testing.T) {
	b, sent := outputs(t)
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(ctx context.Context, word string) ([]dictionary.Entry, error) {
		lookups++
		if word != "book" {
			t.Errorf("lookup word: %q", word)
		}
		return entries(), nil
	}), saveFunc(func(ctx context.Context, userID int64, word, definition string, synonyms []string) (storage.AddMeaningResult, error) {
		saves++
		if userID != 42 || word != "bake" || definition != "To cook in an oven." || !reflect.DeepEqual(synonyms, []string{"cook"}) {
			t.Errorf("wrong saved selection: %d %q %q %v", userID, word, definition, synonyms)
		}
		return storage.AddMeaningResult{WordID: 1, MeaningID: 2, MeaningCreated: true}, nil
	}))
	if handle(f, b, update("/addword")) || handle(f, b, update(" book ")) {
		t.Fatal("flow completed before selection")
	}
	list := strings.Join(sent.texts(), "\n")
	for _, text := range []string{"1.", "2.", "3.", "4.", "noun", "verb", "To reserve."} {
		if !strings.Contains(list, text) {
			t.Errorf("missing %q in choices: %s", text, list)
		}
	}
	if saves != 0 || !handle(f, b, update("4")) || saves != 1 || lookups != 1 {
		t.Fatalf("wrong lifecycle: lookups=%d saves=%d", lookups, saves)
	}
	confirmation := strings.Join(sent.texts()[2:], "\n")
	for _, text := range []string{"bake", "To cook in an oven.", "cook", "https://en.wiktionary.org/wiki/bake"} {
		if !strings.Contains(confirmation, text) {
			t.Errorf("missing %q in saved card: %s", text, confirmation)
		}
	}
}

func TestAddwordInvalidInputDoesNotAdvanceOrSave(t *testing.T) {
	b, _ := outputs(t)
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
		lookups++
		return entries(), nil
	}), saveFunc(func(_ context.Context, _ int64, _, _ string, synonyms []string) (storage.AddMeaningResult, error) {
		saves++
		if len(synonyms) != 0 {
			t.Errorf("group synonyms leaked into definition: %v", synonyms)
		}
		return storage.AddMeaningResult{MeaningCreated: true}, nil
	}))
	handle(f, b, update("/addword"))
	for _, text := range []string{"", "  ", "/unknown"} {
		if handle(f, b, update(text)) {
			t.Fatal("invalid word completed flow")
		}
	}
	if lookups != 0 {
		t.Fatal("invalid input reached dictionary")
	}
	handle(f, b, update("book"))
	for _, text := range []string{"", "0", "-1", "5", "1.5", "text", "/unknown"} {
		if handle(f, b, update(text)) {
			t.Fatal("invalid selection completed flow")
		}
	}
	if lookups != 1 || saves != 0 || !handle(f, b, update("2")) || saves != 1 {
		t.Fatalf("invalid input changed state: lookups=%d saves=%d", lookups, saves)
	}
}

func TestAddwordDictionaryErrorsAllowNewWord(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"not found", dictionary.ErrNotFound}, {"rate limit", dictionary.ErrRateLimited},
		{"unavailable", errors.New("network unavailable")}, {"invalid response", dictionary.ErrInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := outputs(t)
			lookups := 0
			f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
				lookups++
				if lookups == 1 {
					return nil, tc.err
				}
				return entries(), nil
			}), nil)
			handle(f, b, update("/addword"))
			if handle(f, b, update("missing")) || handle(f, b, update("book")) || lookups != 2 {
				t.Fatalf("failed lookup advanced state: %d calls", lookups)
			}
		})
	}
}

func TestAddwordSaveFailureRetriesWithoutLookup(t *testing.T) {
	b, sent := outputs(t)
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { lookups++; return entries(), nil }),
		saveFunc(func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
			saves++
			if saves == 1 {
				return storage.AddMeaningResult{}, errors.New("database unavailable")
			}
			return storage.AddMeaningResult{MeaningID: 1, MeaningCreated: false}, nil
		}))
	handle(f, b, update("/addword"))
	handle(f, b, update("book"))
	if handle(f, b, update("1")) || !handle(f, b, update("1")) || saves != 2 || lookups != 1 {
		t.Fatalf("retry state: lookups=%d saves=%d", lookups, saves)
	}
	if !strings.Contains(strings.Join(sent.texts(), "\n"), "обновлены") {
		t.Fatal("duplicate confirmation does not explain synonym update")
	}
}

func TestAddwordCancelDoesNotSave(t *testing.T) {
	for _, selectState := range []bool{false, true} {
		t.Run(fmt.Sprint(selectState), func(t *testing.T) {
			b, _ := outputs(t)
			f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { return entries(), nil }),
				saveFunc(func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
					t.Error("cancel saved data")
					return storage.AddMeaningResult{}, nil
				}))
			handle(f, b, update("/addword"))
			if selectState {
				handle(f, b, update("book"))
			}
			if !handle(f, b, update("/cancel")) {
				t.Fatal("cancel did not complete flow")
			}
		})
	}
}

func TestAddwordPreservesLongDefinition(t *testing.T) {
	b, sent := outputs(t)
	definition := strings.Repeat("long definition 📖 ", 600)
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
		return []dictionary.Entry{{Word: "book", Meanings: []dictionary.Meaning{{PartOfSpeech: "noun", Definitions: []dictionary.Definition{{Text: definition}}}}}}, nil
	}), saveFunc(func(_ context.Context, _ int64, _, saved string, _ []string) (storage.AddMeaningResult, error) {
		if saved != definition {
			t.Error("saved definition was truncated")
		}
		return storage.AddMeaningResult{MeaningCreated: true}, nil
	}))
	handle(f, b, update("/addword"))
	handle(f, b, update("book"))
	if !strings.Contains(strings.Join(sent.texts(), ""), definition) {
		t.Fatal("displayed definition was truncated")
	}
	if !handle(f, b, update("1")) {
		t.Fatal("long definition not saved")
	}
}

func TestAddwordCanAdvanceWithoutDeliveringOutput(t *testing.T) {
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
		lookups++
		return entries(), nil
	}), saveFunc(func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
		saves++
		return storage.AddMeaningResult{MeaningID: 1, MeaningCreated: true}, nil
	}))
	f.Handle(context.Background(), update("/addword"))
	f.Handle(context.Background(), update("book"))
	result := f.Handle(context.Background(), update("1"))
	if !result.Done || lookups != 1 || saves != 1 || len(result.Texts) == 0 {
		t.Fatalf("state depends on output delivery: %+v lookups=%d saves=%d", result, lookups, saves)
	}
}
