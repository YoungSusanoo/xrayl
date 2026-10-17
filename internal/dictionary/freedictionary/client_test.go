package freedictionary_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"xrayl/internal/dictionary"
	"xrayl/internal/dictionary/freedictionary"
)

const entriesJSON = `[
  {
    "word": "book",
    "meanings": [
      {
        "partOfSpeech": "noun",
        "synonyms": ["publication"],
        "antonyms": ["unwritten work"],
        "definitions": [
          {"definition":"A written work.","example":"Read a book.","synonyms":["volume"],"antonyms":["nonbook"]},
          {"definition":"An account record."}
        ]
      },
      {"partOfSpeech":"verb","definitions":[{"definition":"To reserve."}]}
    ],
    "sourceUrls": ["https://en.wiktionary.org/wiki/book", "https://example.org/book"],
    "license": {"name":"CC BY-SA 3.0","url":"https://creativecommons.org/licenses/by-sa/3.0/"},
    "unknownField": {"ignored":true}
  },
  {"word":"volume","meanings":[{"partOfSpeech":"noun","definitions":[{"definition":"One work in a series."}]}]}
]`

func serverClient(t *testing.T, handler http.HandlerFunc) *freedictionary.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return freedictionary.NewClient(server.URL+"/api/v2/", server.Client())
}

func TestLookupDecodesEntries(t *testing.T) {
	client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/entries/en/book" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected Accept: %q", r.Header.Get("Accept"))
		}
		io.WriteString(w, entriesJSON)
	})
	got, err := client.Lookup(context.Background(), "book")
	if err != nil {
		t.Fatal(err)
	}
	want := []dictionary.Entry{
		{
			Word: "book",
			Meanings: []dictionary.Meaning{
				{PartOfSpeech: "noun", Synonyms: []string{"publication"}, Antonyms: []string{"unwritten work"}, Definitions: []dictionary.Definition{
					{Text: "A written work.", Example: "Read a book.", Synonyms: []string{"volume"}, Antonyms: []string{"nonbook"}},
					{Text: "An account record."},
				}},
				{PartOfSpeech: "verb", Definitions: []dictionary.Definition{{Text: "To reserve."}}},
			},
			SourceURLs: []string{"https://en.wiktionary.org/wiki/book", "https://example.org/book"},
			License: dictionary.License{
				Name: "CC BY-SA 3.0", URL: "https://creativecommons.org/licenses/by-sa/3.0/",
			},
		},
		{Word: "volume", Meanings: []dictionary.Meaning{
			{PartOfSpeech: "noun", Definitions: []dictionary.Definition{{Text: "One work in a series."}}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected entries:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestClientUsesDefaultHTTPClientWhenNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, entriesJSON)
	}))
	defer server.Close()
	client := freedictionary.NewClient(server.URL+"/api/v2", nil)
	if entries, err := client.Lookup(context.Background(), "book"); err != nil || len(entries) != 2 {
		t.Fatalf("default client lookup: %d entries, %v", len(entries), err)
	}
}

func TestLookupEscapesWordAsOnePathSegment(t *testing.T) {
	for _, tc := range []struct{ word, path string }{
		{"  ice cream  ", "/api/v2/entries/en/ice%20cream"},
		{"café", "/api/v2/entries/en/caf%C3%A9"},
		{"a/b?c#d%", "/api/v2/entries/en/a%2Fb%3Fc%23d%25"},
		{"%2F", "/api/v2/entries/en/%252F"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != tc.path || r.URL.RawQuery != "" {
					t.Errorf("wanted path %s without query, got %s", tc.path, r.URL)
				}
				io.WriteString(w, entriesJSON)
			})
			if _, err := client.Lookup(context.Background(), tc.word); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLookupRejectsBlankInputWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, entriesJSON)
	})
	for _, word := range []string{"", " \t\n", "\u2003"} {
		if _, err := client.Lookup(context.Background(), word); !errors.Is(err, dictionary.ErrInvalidWord) {
			t.Fatalf("wanted invalid word for %q, got %v", word, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("sent %d requests for blank input", calls.Load())
	}
}

func TestLookupClassifiesHTTPStatusWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, dictionary.ErrNotFound},
		{http.StatusTooManyRequests, dictionary.ErrRateLimited},
		{http.StatusInternalServerError, nil},
		{http.StatusForbidden, nil},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, "not JSON")
			})
			entries, err := client.Lookup(context.Background(), "book")
			if entries != nil {
				t.Fatalf("HTTP error returned entries: %+v", entries)
			}
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("wanted %v, got %v", tc.want, err)
				}
			} else {
				var statusErr *dictionary.HTTPError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != tc.status || statusErr.Error() == "" {
					t.Fatalf("wanted HTTP status %d, got %v", tc.status, err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("wanted one request, got %d", calls.Load())
			}
		})
	}
}

func TestLookupRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"broken JSON", "["},
		{"object", `{ "word":"book" }`},
		{"null", "null"},
		{"empty array", "[]"},
		{"empty entry", "[{}]"},
		{"empty definitions", `[{"word":"book","meanings":[{"definitions":[]}]}]`},
		{"blank definition", `[{"word":"book","meanings":[{"definitions":[{"definition":"  "}]}]}]`},
		{"missing word", `[{"meanings":[{"definitions":[{"definition":"A written work."}]}]}]`},
		{"wrong field type", `[{"word":"book","meanings":42}]`},
		{"trailing JSON", entriesJSON + " []"},
		{"over size limit", entriesJSON + strings.Repeat(" ", 2<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			})
			entries, err := client.Lookup(context.Background(), "book")
			if !errors.Is(err, dictionary.ErrInvalidResponse) || entries != nil {
				t.Fatalf("wanted invalid response without entries, got %+v, %v", entries, err)
			}
		})
	}
}

func TestLookupAcceptsResponseAtSizeLimit(t *testing.T) {
	client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, entriesJSON+strings.Repeat(" ", (2<<20)-len(entriesJSON)))
	})
	if entries, err := client.Lookup(context.Background(), "book"); err != nil || len(entries) != 2 {
		t.Fatalf("response at size limit rejected: %d entries, %v", len(entries), err)
	}
}

func TestLookupHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	_, err := client.Lookup(ctx, "book")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted canceled context, got %v", err)
	}
}

func TestLookupHonorsEarlierDeadline(t *testing.T) {
	client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.Lookup(ctx, "book"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wanted parent deadline, got %v", err)
	}
}

func TestLookupSupportsConcurrentCalls(t *testing.T) {
	client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		word := strings.TrimPrefix(r.URL.Path, "/api/v2/entries/en/")
		json.NewEncoder(w).Encode([]any{map[string]any{
			"word": word,
			"meanings": []any{map[string]any{
				"partOfSpeech": "noun",
				"definitions":  []any{map[string]string{"definition": "A written work."}},
			}},
		}})
	})
	const workers = 16
	results := make(chan error, workers)
	for i := range workers {
		go func() {
			word := fmt.Sprintf("word%d", i)
			entries, err := client.Lookup(context.Background(), word)
			if err == nil && (len(entries) != 1 || entries[0].Word != word) {
				err = fmt.Errorf("wrong result for %s: %+v", word, entries)
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

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestLookupPreservesNetworkError(t *testing.T) {
	want := errors.New("network unavailable")
	client := freedictionary.NewClient("https://dictionary.example/api/v2", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, want
		}),
	})
	if _, err := client.Lookup(context.Background(), "book"); !errors.Is(err, want) {
		t.Fatalf("lost network error: %v", err)
	}
}

func TestLookupAppliesFiveSecondDeadline(t *testing.T) {
	client := freedictionary.NewClient("https://dictionary.example/api/v2", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			remaining := time.Until(deadline)
			if !ok || remaining < 4*time.Second || remaining > 5*time.Second {
				t.Errorf("wanted five-second deadline, got %v, present=%v", remaining, ok)
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	})
	if _, err := client.Lookup(context.Background(), "book"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wanted client deadline, got %v", err)
	}
}

type observedBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *observedBody) Close() error {
	b.closed = true
	return nil
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestLookupClosesAndLimitsResponseBody(t *testing.T) {
	readError := errors.New("broken response stream")
	for _, tc := range []struct {
		name   string
		status int
		reader io.Reader
		want   error
	}{
		{"success", 200, strings.NewReader(entriesJSON), nil},
		{"not found", 404, strings.NewReader("missing"), dictionary.ErrNotFound},
		{"rate limited", 429, strings.NewReader("limited"), dictionary.ErrRateLimited},
		{"server error", 500, strings.NewReader("failed"), nil},
		{"bad JSON", 200, strings.NewReader("["), dictionary.ErrInvalidResponse},
		{"oversized", 200, strings.NewReader(strings.Repeat(" ", 3<<20)), dictionary.ErrInvalidResponse},
		{"read error", 200, failingReader{readError}, readError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &observedBody{reader: tc.reader}
			client := freedictionary.NewClient("https://dictionary.example/api/v2", &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
				}),
			})
			_, err := client.Lookup(context.Background(), "book")
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("wanted %v, got %v", tc.want, err)
			}
			if !body.closed || body.read > (2<<20)+1 {
				t.Fatalf("body closed=%v, read=%d", body.closed, body.read)
			}
		})
	}
}
