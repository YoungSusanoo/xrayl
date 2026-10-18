//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestUnifiedContextMigration(t *testing.T) {
	db := testDatabase(t)
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	meaning, err := NewStore(db).AddMeaning(ctx, 42, "book", "Printed work.", nil)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := db.QueryRow("SELECT source_id FROM add_source(42,'Dune')").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	var contextExpectations []contextExpectation
	for _, tc := range []struct {
		quote, location any
		want            sql.NullString
	}{
		{"Original.\nSecond line.", "chapter 1", sql.NullString{String: "Original.\nSecond line.\nМесто: chapter 1", Valid: true}},
		{nil, "00:43", sql.NullString{String: "Место: 00:43", Valid: true}},
		{"", "page 2", sql.NullString{String: "Место: page 2", Valid: true}},
		{"  ", "chapter 3", sql.NullString{String: "Место: chapter 3", Valid: true}},
		{"Unchanged.", nil, sql.NullString{String: "Unchanged.", Valid: true}},
		{nil, nil, sql.NullString{}},
		{"", "  ", sql.NullString{Valid: true}},
		{"Keep spaces. ", " page 4 ", sql.NullString{String: "Keep spaces. \nМесто:  page 4 ", Valid: true}},
	} {
		var id int64
		if err := db.QueryRow("INSERT INTO contexts(user_id,meaning_id,source_id,quote,location) VALUES(42,$1,$2,$3,$4) RETURNING id", meaning.MeaningID, sourceID, tc.quote, tc.location).Scan(&id); err != nil {
			t.Fatal(err)
		}
		contextExpectations = append(contextExpectations, contextExpectation{id: id, quote: tc.want})
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var columnExists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='contexts' AND column_name='location')").Scan(&columnExists); err != nil || columnExists {
		t.Fatalf("location still exists: %v %v", columnExists, err)
	}
	verify := func() {
		t.Helper()
		for _, want := range contextExpectations {
			var quote sql.NullString
			var owner, meaningID, source int64
			if err := db.QueryRow("SELECT user_id,meaning_id,source_id,quote FROM contexts WHERE id=$1", want.id).Scan(&owner, &meaningID, &source, &quote); err != nil || owner != 42 || meaningID != meaning.MeaningID || source != sourceID || !reflect.DeepEqual(quote, want.quote) {
				t.Fatalf("row %d: quote=%+v want=%+v owner=%d meaning=%d source=%d err=%v", want.id, quote, want.quote, owner, meaningID, source, err)
			}
		}
	}
	verify()
	var id int64
	if err := db.QueryRow("SELECT add_context($1,$2,$3,$4)", 42, meaning.MeaningID, sourceID, " Free text.\nchapter 2 — 01:23 ").Scan(&id); err != nil || id == 0 {
		t.Fatalf("new SQL signature: %d %v", id, err)
	}
	for _, args := range [][]any{{42, meaning.MeaningID, sourceID, " "}, {nil, meaning.MeaningID, sourceID, "text"}, {42, nil, sourceID, "text"}, {42, meaning.MeaningID, nil, "text"}} {
		_, err := db.Exec("SELECT add_context($1,$2,$3,$4)", args...)
		requireSQLState(t, err, "22023")
	}
	_, err = db.Exec("SELECT add_context($1,$2,$3,$4)", 43, meaning.MeaningID, sourceID, "foreign")
	requireSQLState(t, err, "23503")
	var count int
	if err := db.QueryRow("SELECT count(*) FROM list_word_contexts($1,$2)", 43, meaning.WordID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign contexts: %d %v", count, err)
	}
	var quote string
	if err := db.QueryRow("SELECT quote FROM list_word_contexts($1,$2) WHERE context_id=$3", 42, meaning.WordID, id).Scan(&quote); err != nil || quote != "Free text.\nchapter 2 — 01:23" {
		t.Fatalf("new text: %q %v", quote, err)
	}
	for _, signature := range []string{"add_context(bigint,bigint,bigint,text)", "list_word_contexts(bigint,bigint)"} {
		var definer bool
		if err := db.QueryRow("SELECT prosecdef FROM pg_proc WHERE oid=to_regprocedure($1)", signature).Scan(&definer); err != nil || definer {
			t.Fatalf("invoker: %v %v", definer, err)
		}
	}
	if _, err := p.DownTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	verify()
	var allNull bool
	if err := db.QueryRow("SELECT bool_and(location IS NULL) FROM contexts").Scan(&allNull); err != nil || !allNull {
		t.Fatalf("rollback locations: %v %v", allNull, err)
	}
	var rollbackID int64
	if err := db.QueryRow("SELECT add_context($1,$2,$3,$4,$5)", 42, meaning.MeaningID, sourceID, "After rollback.", "page 9").Scan(&rollbackID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	verify()
	if err := db.QueryRow("SELECT quote FROM contexts WHERE id=$1", rollbackID).Scan(&quote); err != nil || quote != "After rollback.\nМесто: page 9" {
		t.Fatalf("reapply: %q %v", quote, err)
	}
}

type contextExpectation struct {
	id    int64
	quote sql.NullString
}
