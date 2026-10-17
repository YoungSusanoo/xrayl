package postgres

import (
	"context"
	"database/sql"
	"time"

	"xrayl/internal/storage"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) AddMeaning(ctx context.Context, userID int64, word, definition string, synonyms []string) (storage.AddMeaningResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if synonyms == nil {
		synonyms = []string{}
	}
	var result storage.AddMeaningResult
	err := s.db.QueryRowContext(ctx,
		"SELECT word_id, meaning_id, meaning_created FROM add_word_meaning($1, $2, $3, $4)",
		userID, word, definition, synonyms,
	).Scan(&result.WordID, &result.MeaningID, &result.MeaningCreated)
	return result, err
}
