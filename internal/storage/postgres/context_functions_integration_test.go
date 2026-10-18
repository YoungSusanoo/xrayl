//go:build integration

package postgres

import (
	"context"
	"testing"
)

func TestSourceAndContextFunctions(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	var sourceID int64
	var name string
	var created bool
	if err := db.QueryRow("SELECT source_id, name, source_created FROM add_source(42,' Dune ')").Scan(&sourceID, &name, &created); err != nil || name != "Dune" || !created {
		t.Fatalf("source: %d %q %v %v", sourceID, name, created, err)
	}
	var duplicate int64
	if err := db.QueryRow("SELECT source_id, source_created FROM add_source(42,'DUNE')").Scan(&duplicate, &created); err != nil || duplicate != sourceID || created {
		t.Fatalf("duplicate source: %d, %v, %v", duplicate, created, err)
	}
	for _, query := range []string{"SELECT * FROM add_source(42,'apple')", "SELECT * FROM add_source(43,'Hidden')"} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	var firstName string
	if err := db.QueryRow("SELECT name FROM list_sources(42,1,0)").Scan(&firstName); err != nil || firstName != "apple" {
		t.Fatalf("source order: %s, %v", firstName, err)
	}
	meaning, err := NewStore(db).AddMeaning(ctx, 42, "book", "Printed work.", nil)
	if err != nil {
		t.Fatal(err)
	}
	var contextID int64
	if err := db.QueryRow("SELECT add_context($1,$2,$3,$4,$5)", 42, meaning.MeaningID, sourceID, " A book. ", " page 1 ").Scan(&contextID); err != nil || contextID == 0 {
		t.Fatalf("context: %d, %v", contextID, err)
	}
	var quote, location, sourceName string
	if err := db.QueryRow("SELECT quote,location,source_name FROM list_word_contexts($1,$2)", 42, meaning.WordID).Scan(&quote, &location, &sourceName); err != nil || quote != "A book." || location != "page 1" || sourceName != "Dune" {
		t.Fatalf("context read: %q %q %q %v", quote, location, sourceName, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM list_word_contexts($1,$2)", 43, meaning.WordID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("owner isolation: %d %v", count, err)
	}
	for _, query := range []string{"SELECT * FROM add_source(42,' ')", "SELECT * FROM add_source(NULL,'Title')", "SELECT * FROM list_sources(42,0,0)", "SELECT * FROM list_sources(42,21,-1)"} {
		_, err := db.Exec(query)
		requireSQLState(t, err, "22023")
	}
	_, err = db.Exec("SELECT add_context($1,$2,$3,$4,$5)", 42, meaning.MeaningID, sourceID, " ", nil)
	requireSQLState(t, err, "22023")
	_, err = db.Exec("SELECT add_context($1,$2,$3,$4,$5)", 43, meaning.MeaningID, sourceID, "Foreign", nil)
	requireSQLState(t, err, "23503")
	var foreignSource int64
	if err := db.QueryRow("SELECT id FROM sources WHERE user_id=43").Scan(&foreignSource); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("SELECT add_context($1,$2,$3,$4,$5)", 42, meaning.MeaningID, foreignSource, "Foreign", nil)
	requireSQLState(t, err, "23503")
	if _, err := db.Exec("SELECT add_context($1,$2,$3,$4,$5)", 42, meaning.MeaningID, sourceID, "Another quote.", " "); err != nil {
		t.Fatal(err)
	}
	var nullLocation bool
	if err := db.QueryRow("SELECT location IS NULL FROM contexts WHERE quote='Another quote.'").Scan(&nullLocation); err != nil || !nullLocation {
		t.Fatalf("null location: %v %v", nullLocation, err)
	}
	for _, signature := range []string{"add_source(bigint,text)", "list_sources(bigint,integer,integer)", "add_context(bigint,bigint,bigint,text,text)", "list_word_contexts(bigint,bigint)"} {
		var definer bool
		if err := db.QueryRow("SELECT prosecdef FROM pg_proc WHERE oid=to_regprocedure($1)", signature).Scan(&definer); err != nil || definer {
			t.Fatalf("security %s: %v %v", signature, definer, err)
		}
	}
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM contexts WHERE user_id=42").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback lost contexts: %d %v", count, err)
	}
	var missing bool
	if err := db.QueryRow("SELECT to_regprocedure('add_source(bigint,text)') IS NULL").Scan(&missing); err != nil || !missing {
		t.Fatalf("rollback functions: %v %v", missing, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
}
