package flows_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type sourceWriterFunc func(context.Context, int64, string) (storage.AddSourceResult, error)

func (f sourceWriterFunc) AddSource(ctx context.Context, user int64, name string) (storage.AddSourceResult, error) {
	return f(ctx, user, name)
}

type sourceReaderFunc func(context.Context, int64, int, int) ([]storage.Source, error)

func (f sourceReaderFunc) ListSources(ctx context.Context, user int64, limit, offset int) ([]storage.Source, error) {
	return f(ctx, user, limit, offset)
}

func sourceFixture(count int) []storage.Source {
	got := make([]storage.Source, count)
	for i := range got {
		got[i] = storage.Source{ID: int64(100 + i), Name: fmt.Sprintf("source%02d", i)}
	}
	return got
}

func TestAddsourceCreatesRetriesAndCancels(t *testing.T) {
	for _, created := range []bool{true, false} {
		t.Run(fmt.Sprint(created), func(t *testing.T) {
			b, sent := telegram(t)
			calls := 0
			f := flows.NewAddsource(sourceWriterFunc(func(ctx context.Context, user int64, name string) (storage.AddSourceResult, error) {
				checkReadContext(t, ctx)
				calls++
				if user != 42 || name != "Dune" {
					t.Errorf("save source: %d %q", user, name)
				}
				if calls == 1 {
					return storage.AddSourceResult{}, errors.New("unavailable")
				}
				return storage.AddSourceResult{Source: storage.Source{ID: 1, Name: "Dune"}, Created: created}, nil
			}))
			f.Handle(context.Background(), b, update("/addsource"))
			for _, text := range []string{"", " ", "/unknown"} {
				if f.Handle(context.Background(), b, update(text)) {
					t.Fatal("invalid source ended flow")
				}
			}
			if calls != 0 {
				t.Fatal("invalid input saved source")
			}
			if f.Handle(context.Background(), b, update(" Dune ")) {
				t.Fatal("error ended flow")
			}
			sent.failNext()
			if !f.Handle(context.Background(), b, update("Dune")) || calls != 2 {
				t.Fatal("source not saved")
			}
			last := sent.texts()[len(sent.texts())-1]
			if !strings.Contains(last, "Dune") || (!created && !strings.Contains(last, "уже есть")) {
				t.Fatalf("confirmation: %s", last)
			}
		})
	}
	f := flows.NewAddsource(nil)
	for _, u := range []*models.Update{nil, {}, {Message: &models.Message{}}} {
		if f.Handle(context.Background(), nil, u) {
			t.Fatal("invalid update ended")
		}
	}
	b, _ := telegram(t)
	if !f.Handle(context.Background(), b, update("/cancel")) {
		t.Fatal("cancel failed")
	}
}

func TestSourcesPagesErrorsAndEmpty(t *testing.T) {
	b, sent := telegram(t)
	all := sourceFixture(21)
	var offsets []int
	calls := 0
	f := flows.NewSources(sourceReaderFunc(func(ctx context.Context, user int64, limit, offset int) ([]storage.Source, error) {
		checkReadContext(t, ctx)
		if user != 42 || limit != 21 {
			t.Error("wrong page query")
		}
		calls++
		offsets = append(offsets, offset)
		if calls == 2 {
			return nil, errors.New("unavailable")
		}
		return all[offset:min(offset+limit, len(all))], nil
	}))
	for _, text := range []string{"/sources", "/prev", "/next", "/next", "/next", "/prev", "1"} {
		if f.Handle(context.Background(), b, update(text)) {
			t.Fatal("browsing ended")
		}
	}
	if !reflect.DeepEqual(offsets, []int{0, 20, 20, 0}) {
		t.Fatalf("offsets: %v", offsets)
	}
	if first := sent.texts()[0]; !strings.Contains(first, "1. source00") || strings.Contains(first, "source20") || strings.Contains(first, "Пришли номер") {
		t.Fatalf("list: %s", first)
	}
	if !f.Handle(context.Background(), b, update("/cancel")) {
		t.Fatal("cancel failed")
	}
	for _, count := range []int{0, 20} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			b, sent := telegram(t)
			f := flows.NewSources(sourceReaderFunc(func(context.Context, int64, int, int) ([]storage.Source, error) { return sourceFixture(count), nil }))
			if done := f.Handle(context.Background(), b, update("/sources")); done != (count == 0) {
				t.Fatal("empty lifecycle")
			}
			if count == 0 && !strings.Contains(sent.texts()[0], "/addsource") {
				t.Fatal("missing hint")
			}
			if strings.Contains(sent.texts()[0], "/next") {
				t.Fatal("last page has next")
			}
		})
	}
}
