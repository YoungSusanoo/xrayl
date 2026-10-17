package dictionary

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidWord     = errors.New("dictionary: word must not be blank")
	ErrNotFound        = errors.New("dictionary: word not found")
	ErrRateLimited     = errors.New("dictionary: rate limit exceeded")
	ErrInvalidResponse = errors.New("dictionary: invalid response")
)

type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("dictionary: HTTP status %d", e.StatusCode)
}
