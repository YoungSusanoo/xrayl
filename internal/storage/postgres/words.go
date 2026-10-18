package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"xrayl/internal/storage"
)

func (s *Store) ListWords(ctx context.Context, userID int64, limit, offset int) ([]storage.Word, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT word_id, word FROM list_words($1,$2,$3)", userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var words []storage.Word
	for rows.Next() {
		var word storage.Word
		if err := rows.Scan(&word.ID, &word.Text); err != nil {
			return nil, err
		}
		words = append(words, word)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return words, nil
}

func (s *Store) ListMeanings(ctx context.Context, userID, wordID int64) ([]storage.Meaning, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT meaning_id, definition, synonyms FROM list_word_meanings($1,$2)", userID, wordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types := pgtype.NewMap()
	var meanings []storage.Meaning
	for rows.Next() {
		var meaning storage.Meaning
		if err := rows.Scan(&meaning.ID, &meaning.Definition, types.SQLScanner(&meaning.Synonyms)); err != nil {
			return nil, err
		}
		meanings = append(meanings, meaning)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return meanings, nil
}
