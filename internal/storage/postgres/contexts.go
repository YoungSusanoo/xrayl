package postgres

import (
	"context"
	"database/sql"
	"time"

	"xrayl/internal/storage"
)

func (s *Store) AddSource(ctx context.Context, userID int64, name string) (storage.AddSourceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result storage.AddSourceResult
	err := s.db.QueryRowContext(ctx, "SELECT source_id,name,source_created FROM add_source($1,$2)", userID, name).Scan(&result.Source.ID, &result.Source.Name, &result.Created)
	return result, err
}

func (s *Store) ListSources(ctx context.Context, userID int64, limit, offset int) ([]storage.Source, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT source_id,name FROM list_sources($1,$2,$3)", userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []storage.Source
	for rows.Next() {
		var source storage.Source
		if err := rows.Scan(&source.ID, &source.Name); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Store) AddContext(ctx context.Context, userID, meaningID, sourceID int64, quote string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT add_context($1,$2,$3,$4)", userID, meaningID, sourceID, quote).Scan(&id)
	return id, err
}

func (s *Store) ListWordContexts(ctx context.Context, userID, wordID int64) ([]storage.WordContext, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT context_id,meaning_id,source_id,source_name,quote FROM list_word_contexts($1,$2)", userID, wordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contexts []storage.WordContext
	for rows.Next() {
		var item storage.WordContext
		var quote sql.NullString
		if err := rows.Scan(&item.ID, &item.MeaningID, &item.Source.ID, &item.Source.Name, &quote); err != nil {
			return nil, err
		}
		item.Quote = quote.String
		contexts = append(contexts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return contexts, nil
}
