package freedictionaryapi_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"xrayl/internal/dictionary"
	"xrayl/internal/dictionary/freedictionaryapi"
)

const responseJSON = `{
  "word":"fever",
  "entries":[
    {"language":{"code":"en"},"partOfSpeech":"noun","synonyms":["group synonym"],"antonyms":["group antonym"],"senses":[
      {"definition":"A high body temperature.","examples":["First example.","Second example."],"synonyms":["pyrexia"],"antonyms":["normal temperature"],"subsenses":[
        {"definition":"A related sense.","subsenses":[{"definition":"A nested sense."}]}
      ]},
      {"definition":"","subsenses":[{"definition":"A sense under a grouping node."}]}
    ]},
    {"language":{"code":"en"},"partOfSpeech":"verb","senses":[{"definition":"To cause excitement."}]},
    {"language":{"code":"fr"},"partOfSpeech":"noun","senses":[{"definition":"Must not appear in English results."}]}
  ],
  "source":{"url":"https://en.wiktionary.org/wiki/fever","license":{"name":"CC BY-SA 4.0","url":"https://creativecommons.org/licenses/by-sa/4.0/"}},
  "ignored":true
}`

const minimalJSON = `{"word":"fever","entries":[{"language":{"code":"en"},"partOfSpeech":"noun","senses":[{"definition":"A high body temperature."}]}]}`
const emptyJSON = `{"word":"missing","entries":[]}`

func localClient(t *testing.T, handler http.HandlerFunc) *freedictionaryapi.Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return freedictionaryapi.NewClient(s.URL+"/api/v1/", s.Client())
}

func TestLookupMapsEntriesAndNestedSenses(t *testing.T) {
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/api/v1/entries/en/fever" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, responseJSON)
	})
	got, err := c.Lookup(context.Background(), " fever ")
	if err != nil {
		t.Fatal(err)
	}
	want := []dictionary.Entry{
		{Word: "fever", Meanings: []dictionary.Meaning{{PartOfSpeech: "noun", Synonyms: []string{"group synonym"}, Antonyms: []string{"group antonym"}, Definitions: []dictionary.Definition{
			{Text: "A high body temperature.", Example: "First example.\nSecond example.", Synonyms: []string{"pyrexia"}, Antonyms: []string{"normal temperature"}},
			{Text: "A related sense."}, {Text: "A nested sense."}, {Text: "A sense under a grouping node."},
		}}}, SourceURLs: []string{"https://en.wiktionary.org/wiki/fever", "https://freedictionaryapi.com"}, License: dictionary.License{Name: "CC BY-SA 4.0", URL: "https://creativecommons.org/licenses/by-sa/4.0/"}},
		{Word: "fever", Meanings: []dictionary.Meaning{{PartOfSpeech: "verb", Definitions: []dictionary.Definition{{Text: "To cause excitement."}}}}, SourceURLs: []string{"https://en.wiktionary.org/wiki/fever", "https://freedictionaryapi.com"}, License: dictionary.License{Name: "CC BY-SA 4.0", URL: "https://creativecommons.org/licenses/by-sa/4.0/"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func TestLookupLowercaseFallbackOnlyForEmptyResults(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/api/v1/entries/en/Fever" {
			io.WriteString(w, emptyJSON)
			return
		}
		io.WriteString(w, minimalJSON)
	})
	got, err := c.Lookup(context.Background(), "Fever")
	if err != nil || len(got) != 1 || got[0].Word != "fever" {
		t.Fatalf("fallback result: %+v, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(paths, []string{"/api/v1/entries/en/Fever", "/api/v1/entries/en/fever"}) {
		t.Fatalf("fallback paths: %v", paths)
	}
}

func TestLookupPreservesSuccessfulExactCase(t *testing.T) {
	var calls atomic.Int32
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/entries/en/May" {
			t.Errorf("lost case: %s", r.URL.Path)
		}
		io.WriteString(w, `{"word":"May","entries":[{"language":{"code":"en"},"partOfSpeech":"proper noun","senses":[{"definition":"A month."}]}]}`)
	})
	got, err := c.Lookup(context.Background(), "May")
	if err != nil || len(got) != 1 || got[0].Word != "May" || calls.Load() != 1 {
		t.Fatalf("exact-case lookup: %+v, %v, %d requests", got, err, calls.Load())
	}
}

func TestLookupClassifiesResponsesWithoutNetworkRetries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"missing", 200, emptyJSON, dictionary.ErrNotFound},
		{"bad JSON", 200, "[", dictionary.ErrInvalidResponse},
		{"wrong root", 200, "[]", dictionary.ErrInvalidResponse},
		{"missing fields", 200, "{}", dictionary.ErrInvalidResponse},
		{"null entries", 200, `{"word":"fever","entries":null}`, dictionary.ErrInvalidResponse},
		{"missing language", 200, `{"word":"fever","entries":[{"senses":[{"definition":"An invalid entry."}]}]}`, dictionary.ErrInvalidResponse},
		{"foreign entries", 200, `{"word":"fever","entries":[{"language":{"code":"fr"},"senses":[{"definition":"A foreign sense."}]}]}`, dictionary.ErrInvalidResponse},
		{"no definitions", 200, `{"word":"fever","entries":[{"language":{"code":"en"},"senses":[]}]}`, dictionary.ErrInvalidResponse},
		{"trailing JSON", 200, minimalJSON + " {}", dictionary.ErrInvalidResponse},
		{"oversize", 200, minimalJSON + strings.Repeat(" ", 2<<20), dictionary.ErrInvalidResponse},
		{"rate limit", 429, "limited", dictionary.ErrRateLimited},
		{"HTML 404", 404, "<html>404 page</html>", nil},
		{"unavailable", 522, "error", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			got, err := c.Lookup(context.Background(), "fever")
			if got != nil {
				t.Fatalf("error returned entries: %+v", got)
			}
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("wanted %v, got %v", tc.want, err)
				}
			} else {
				var statusErr *dictionary.HTTPError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != tc.status {
					t.Fatalf("wanted status %d, got %v", tc.status, err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("unexpected retry: %d calls", calls.Load())
			}
		})
	}
}

func TestLookupDoesNotFallbackOnUpstreamFailure(t *testing.T) {
	for _, status := range []int{404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			c := localClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(status) })
			if _, err := c.Lookup(context.Background(), "Fever"); err == nil || calls.Load() != 1 {
				t.Fatalf("upstream failure caused fallback: err=%v, calls=%d", err, calls.Load())
			}
		})
	}
}

func TestLookupDoesNotFallbackOnInvalidResponse(t *testing.T) {
	for _, body := range []string{"{", `{"word":"Fever","entries":[{"language":{"code":"fr"},"senses":[{"definition":"A foreign sense."}]}]}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			c := localClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, body) })
			_, err := c.Lookup(context.Background(), "Fever")
			if !errors.Is(err, dictionary.ErrInvalidResponse) || calls.Load() != 1 {
				t.Fatalf("invalid response: %v, %d requests", err, calls.Load())
			}
		})
	}
}

func TestLookupEscapesWordAndRejectsBlankInput(t *testing.T) {
	var calls atomic.Int32
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.EscapedPath() != "/api/v1/entries/en/a%2Fb%3Fc%23d%25" || r.URL.RawQuery != "" {
			t.Errorf("unescaped word: %s", r.URL)
		}
		io.WriteString(w, minimalJSON)
	})
	for _, word := range []string{"", " \t\n", "\u2003"} {
		if _, err := c.Lookup(context.Background(), word); !errors.Is(err, dictionary.ErrInvalidWord) {
			t.Fatalf("blank word: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("blank input sent a request")
	}
	if _, err := c.Lookup(context.Background(), " a/b?c#d% "); err != nil {
		t.Fatal(err)
	}
}

func TestLookupHonorsParentDeadline(t *testing.T) {
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.Lookup(ctx, "fever"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error: %v", err)
	}
}

func TestLookupConcurrentCalls(t *testing.T) {
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, minimalJSON) })
	const workers = 16
	results := make(chan error, workers)
	for range workers {
		go func() {
			got, err := c.Lookup(context.Background(), "fever")
			if err == nil && (len(got) != 1 || got[0].Word != "fever") {
				err = fmt.Errorf("wrong entries: %+v", got)
			}
			results <- err
		}()
	}
	for range workers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type observedBody struct {
	io.Reader
	closed bool
}

func (b *observedBody) Close() error { b.closed = true; return nil }

type failedReader struct{ err error }

func (r failedReader) Read([]byte) (int, error) { return 0, r.err }

func TestLookupSharesDeadlineAcrossFallbackAndClosesBodies(t *testing.T) {
	var bodies []*observedBody
	var deadlines []time.Time
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("missing five-second deadline")
		}
		deadlines = append(deadlines, deadline)
		body := emptyJSON
		if len(deadlines) == 2 {
			body = minimalJSON
		}
		observed := &observedBody{Reader: strings.NewReader(body)}
		bodies = append(bodies, observed)
		return &http.Response{StatusCode: http.StatusOK, Body: observed}, nil
	})}
	if _, err := freedictionaryapi.NewClient("https://example.test", client).Lookup(context.Background(), "Fever"); err != nil {
		t.Fatal(err)
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("fallback reset deadline: %v", deadlines)
	}
	for _, body := range bodies {
		if !body.closed {
			t.Fatal("response body was not closed")
		}
	}
}

func TestLookupPreservesReadErrorAndClosesBody(t *testing.T) {
	want := errors.New("broken response body")
	body := &observedBody{Reader: failedReader{err: want}}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}
	_, err := freedictionaryapi.NewClient("https://example.test", client).Lookup(context.Background(), "Fever")
	if !errors.Is(err, want) || !body.closed {
		t.Fatalf("read failure: %v, body closed: %v", err, body.closed)
	}
}
