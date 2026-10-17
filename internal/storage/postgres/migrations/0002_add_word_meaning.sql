-- +goose Up

CREATE UNIQUE INDEX meanings_user_word_definition_unique
    ON meanings (user_id, word_id, lower(btrim(definition)));

-- +goose StatementBegin
CREATE FUNCTION add_word_meaning(
    p_user_id BIGINT,
    p_word TEXT,
    p_definition TEXT
)
RETURNS TABLE (
    word_id BIGINT,
    meaning_id BIGINT,
    meaning_created BOOLEAN
)
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
AS $$
DECLARE
    v_word TEXT := btrim(p_word);
    v_definition TEXT := btrim(p_definition);
    v_word_id BIGINT;
    v_meaning_id BIGINT;
    v_created BOOLEAN;
BEGIN
    IF p_user_id IS NULL OR v_word IS NULL OR v_word = ''
        OR v_definition IS NULL OR v_definition = '' THEN
        RAISE EXCEPTION 'user_id, word and definition must be non-null; texts must not be blank'
            USING ERRCODE = '22023';
    END IF;

    INSERT INTO users (id) VALUES (p_user_id)
        ON CONFLICT (id) DO NOTHING;

    INSERT INTO words (user_id, text) VALUES (p_user_id, v_word)
        ON CONFLICT DO NOTHING
        RETURNING words.id INTO v_word_id;

    IF v_word_id IS NULL THEN
        -- A separate statement sees a concurrent insert after ON CONFLICT
        -- has waited for its commit at READ COMMITTED isolation.
        SELECT w.id INTO v_word_id FROM words AS w
            WHERE w.user_id = p_user_id AND lower(w.text) = lower(v_word);
    END IF;

    INSERT INTO meanings (user_id, word_id, definition)
        VALUES (p_user_id, v_word_id, v_definition)
        ON CONFLICT DO NOTHING
        RETURNING meanings.id INTO v_meaning_id;
    v_created := FOUND;

    IF NOT v_created THEN
        SELECT m.id INTO v_meaning_id FROM meanings AS m
            WHERE m.user_id = p_user_id AND m.word_id = v_word_id
                AND lower(btrim(m.definition)) = lower(v_definition);
    END IF;

    RETURN QUERY SELECT v_word_id, v_meaning_id, v_created;
END;
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION add_word_meaning(BIGINT, TEXT, TEXT);
DROP INDEX meanings_user_word_definition_unique;
