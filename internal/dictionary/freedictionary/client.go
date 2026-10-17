package freedictionary

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"xrayl/internal/dictionary"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
	}
}

func (c *Client) Lookup(ctx context.Context, word string) ([]dictionary.Entry, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, dictionary.ErrInvalidWord
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	endpoint := c.baseURL + "/entries/en/" + url.PathEscape(word)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create dictionary request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup word: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return decodeEntries(resp.Body)
	case http.StatusNotFound:
		return nil, dictionary.ErrNotFound
	case http.StatusTooManyRequests:
		return nil, dictionary.ErrRateLimited
	default:
		return nil, &dictionary.HTTPError{StatusCode: resp.StatusCode}
	}
}
