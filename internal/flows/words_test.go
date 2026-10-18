package flows_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"

	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type readStore struct {
	words    func(context.Context, int64, int, int) ([]storage.Word, error)
	meanings func(context.Context, int64, int64) ([]storage.Meaning, error)
}

func (s readStore) ListWordContexts(context.Context, int64, int64) ([]storage.WordContext, error) {
	return nil, nil
}

func (s readStore) ListWords(ctx context.Context, user int64, limit, offset int) ([]storage.Word, error) {
	return s.words(ctx, user, limit, offset)
}
func (s readStore) ListMeanings(ctx context.Context, user, word int64) ([]storage.Meaning, error) {
	return s.meanings(ctx, user, word)
}

func wordFixture(count int) []storage.Word {
	words := make([]storage.Word, count)
	for i := range words {
		words[i] = storage.Word{ID: int64(100 + i), Text: fmt.Sprintf("word%02d", i)}
	}
	return words
}

func checkReadContext(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		t.Error("read must have a five-second deadline")
	}
}

func TestWordsPagesAndRepeatedSelection(t *testing.T) {
	b, sent := telegram(t)
	all := wordFixture(41)
	var offsets []int
	var selected []int64
	f := flows.NewWords(readStore{
		words: func(ctx context.Context, user int64, limit, offset int) ([]storage.Word, error) {
			checkReadContext(t, ctx)
			if user != 42 || limit != 21 {
				t.Errorf("wrong page query: %d, %d", user, limit)
			}
			offsets = append(offsets, offset)
			return all[offset:min(offset+limit, len(all))], nil
		},
		meanings: func(ctx context.Context, user, word int64) ([]storage.Meaning, error) {
			checkReadContext(t, ctx)
			if user != 42 {
				t.Error("wrong meaning owner")
			}
			selected = append(selected, word)
			return []storage.Meaning{{ID: 1, Definition: "First meaning.", Synonyms: []string{"volume", "tome"}}, {ID: 2, Definition: "Second meaning."}}, nil
		},
	})
	for _, text := range []string{"/words", "/prev", "1", "20", "/next", "1", "/next", "/next", "1", "/prev", "2"} {
		if f.Handle(context.Background(), b, update(text)) {
			t.Fatalf("browsing completed on %s", text)
		}
	}
	if !reflect.DeepEqual(offsets, []int{0, 20, 40, 20}) || !reflect.DeepEqual(selected, []int64{100, 119, 120, 140, 121}) {
		t.Fatalf("paging offsets=%v, selections=%v", offsets, selected)
	}
	texts := sent.texts()
	if !strings.Contains(texts[0], "1. word00") || !strings.Contains(texts[0], "20. word19") || strings.Contains(texts[0], "word20") || !strings.Contains(texts[0], "/next") || strings.Contains(texts[0], "/prev") {
		t.Fatalf("first page: %s", texts[0])
	}
	for _, want := range []string{"First meaning.", "Second meaning.", "volume, tome", "Страница 3", "последняя", "первая"} {
		if !strings.Contains(strings.Join(texts, "\n"), want) {
			t.Errorf("missing %q", want)
		}
	}
	if !f.Handle(context.Background(), b, update("/cancel")) {
		t.Fatal("cancel did not finish")
	}
}

func TestWordsEmptyAndInvalidInput(t *testing.T) {
	for _, count := range []int{0, 1, 20} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			b, sent := telegram(t)
			reads := 0
			f := flows.NewWords(readStore{words: func(context.Context, int64, int, int) ([]storage.Word, error) { return wordFixture(count), nil }, meanings: func(context.Context, int64, int64) ([]storage.Meaning, error) { reads++; return nil, nil }})
			done := f.Handle(context.Background(), b, update("/words"))
			if done != (count == 0) {
				t.Fatalf("empty lifecycle: %v", done)
			}
			if count == 0 {
				if !strings.Contains(strings.Join(sent.texts(), ""), "/addword") {
					t.Fatal("missing addword hint")
				}
				return
			}
			for _, input := range []string{"", "0", "-1", "21", "1.5", "hello", "/unknown", "/next", "/prev"} {
				if f.Handle(context.Background(), b, update(input)) {
					t.Fatal("invalid input ended browsing")
				}
			}
			if reads != 0 {
				t.Fatal("invalid selection reached storage")
			}
			if strings.Contains(sent.texts()[0], "/next") {
				t.Fatal("last page advertises next")
			}
			f.Handle(context.Background(), b, update("1"))
			if reads != 1 || !strings.Contains(strings.Join(sent.texts(), ""), "Нет сохранённых значений") {
				t.Fatal("missing meaning was not explained")
			}
		})
	}
}

func TestWordsReadFailuresPreserveSelection(t *testing.T) {
	b, _ := telegram(t)
	pageCalls, meaningCalls := 0, 0
	var selected []int64
	f := flows.NewWords(readStore{words: func(context.Context, int64, int, int) ([]storage.Word, error) {
		pageCalls++
		if pageCalls == 1 || pageCalls == 3 {
			return nil, errors.New("unavailable")
		}
		return wordFixture(21), nil
	}, meanings: func(_ context.Context, _ int64, word int64) ([]storage.Meaning, error) {
		meaningCalls++
		selected = append(selected, word)
		if meaningCalls == 1 {
			return nil, errors.New("unavailable")
		}
		return []storage.Meaning{{Definition: "A meaning."}}, nil
	}})
	for _, input := range []string{"/words", "retry", "/next", "2", "2"} {
		if f.Handle(context.Background(), b, update(input)) {
			t.Fatal("read failure ended browsing")
		}
	}
	if pageCalls != 3 || !reflect.DeepEqual(selected, []int64{101, 101}) {
		t.Fatalf("failed read lost page: pages=%d selected=%v", pageCalls, selected)
	}
}

func TestWordsSendFailureKeepsLoadedPage(t *testing.T) {
	b, sent := telegram(t)
	all := wordFixture(21)
	var selected []int64
	f := flows.NewWords(readStore{words: func(_ context.Context, _ int64, limit, offset int) ([]storage.Word, error) {
		return all[offset:min(offset+limit, len(all))], nil
	}, meanings: func(_ context.Context, _ int64, word int64) ([]storage.Meaning, error) {
		selected = append(selected, word)
		return []storage.Meaning{{Definition: "Meaning."}}, nil
	}})
	sent.failNext()
	f.Handle(context.Background(), b, update("/words"))
	f.Handle(context.Background(), b, update("1"))
	sent.failNext()
	f.Handle(context.Background(), b, update("/next"))
	f.Handle(context.Background(), b, update("1"))
	sent.failNext()
	f.Handle(context.Background(), b, update("1"))
	f.Handle(context.Background(), b, update("1"))
	if !reflect.DeepEqual(selected, []int64{100, 120, 120, 120}) {
		t.Fatalf("failed send lost selection: %v", selected)
	}
}

func TestWordsInvalidUpdatesAndCancelBeforeLoading(t *testing.T) {
	f := flows.NewWords(nil)
	for _, u := range []*models.Update{nil, {}, {Message: &models.Message{}}} {
		if f.Handle(context.Background(), nil, u) {
			t.Fatal("invalid update ended flow")
		}
	}
	b, _ := telegram(t)
	if !f.Handle(context.Background(), b, update("/cancel")) {
		t.Fatal("initial cancellation failed")
	}
}

func TestWordsLongMeaningsAndSynonyms(t *testing.T) {
	b, sent := telegram(t)
	definition := strings.Repeat("A long definition 📖. ", 600)
	f := flows.NewWords(readStore{words: func(context.Context, int64, int, int) ([]storage.Word, error) {
		return []storage.Word{{ID: 1, Text: "book"}}, nil
	}, meanings: func(context.Context, int64, int64) ([]storage.Meaning, error) {
		return []storage.Meaning{{Definition: definition, Synonyms: []string{"volume", "tome"}}, {Definition: "Another meaning."}}, nil
	}})
	f.Handle(context.Background(), b, update("/words"))
	f.Handle(context.Background(), b, update("1"))
	joined := strings.Join(sent.texts(), "")
	if !strings.Contains(joined, definition) || !strings.Contains(joined, "volume, tome") || !strings.Contains(joined, "Another meaning.") {
		t.Fatal("word card truncated")
	}
	for _, text := range sent.texts() {
		if len(utf16.Encode([]rune(text))) > 3000 {
			t.Fatal("oversized message")
		}
	}
}
