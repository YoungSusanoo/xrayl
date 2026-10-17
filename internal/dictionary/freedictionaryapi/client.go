package freedictionaryapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"xrayl/internal/dictionary"
)

const BaseURL = "https://freedictionaryapi.com/api/v1"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: client}
}

func (c *Client) Lookup(ctx context.Context, word string) ([]dictionary.Entry, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, dictionary.ErrInvalidWord
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries, err := c.lookup(ctx, word)
	if lower := strings.ToLower(word); errors.Is(err, dictionary.ErrNotFound) && lower != word {
		return c.lookup(ctx, lower)
	}
	return entries, err
}

func (c *Client) lookup(ctx context.Context, word string) ([]dictionary.Entry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/entries/en/"+url.PathEscape(word), nil)
	if err != nil {
		return nil, fmt.Errorf("create dictionary request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup word: %w", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return decodeEntries(response.Body)
	case http.StatusTooManyRequests:
		return nil, dictionary.ErrRateLimited
	default:
		return nil, &dictionary.HTTPError{StatusCode: response.StatusCode}
	}
}
