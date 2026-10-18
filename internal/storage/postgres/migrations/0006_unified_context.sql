-- +goose Up

UPDATE contexts
SET quote = CASE
    WHEN NULLIF(btrim(quote), '') IS NULL THEN 'Место: ' || location
    ELSE quote || E'\nМесто: ' || location
END
WHERE NULLIF(btrim(location), '') IS NOT NULL;

DROP FUNCTION list_word_contexts(BIGINT, BIGINT);
DROP FUNCTION add_context(BIGINT, BIGINT, BIGINT, TEXT, TEXT);
ALTER TABLE contexts DROP COLUMN location;

-- +goose StatementBegin
CREATE FUNCTION add_context(p_user_id BIGINT, p_meaning_id BIGINT, p_source_id BIGINT, p_quote TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
AS $$
DECLARE
    v_id BIGINT;
    v_quote TEXT := btrim(p_quote);
BEGIN
    IF p_user_id IS NULL OR p_meaning_id IS NULL OR p_source_id IS NULL
        OR v_quote IS NULL OR v_quote = '' THEN
        RAISE EXCEPTION 'user, meaning, source and nonblank context are required' USING ERRCODE = '22023';
    END IF;
    INSERT INTO contexts(user_id, meaning_id, source_id, quote)
        VALUES (p_user_id, p_meaning_id, p_source_id, v_quote)
        RETURNING id INTO v_id;
    RETURN v_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION list_word_contexts(p_user_id BIGINT, p_word_id BIGINT)
RETURNS TABLE (context_id BIGINT, meaning_id BIGINT, source_id BIGINT, source_name TEXT, quote TEXT)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
AS $$
BEGIN
    IF p_user_id IS NULL OR p_word_id IS NULL THEN
        RAISE EXCEPTION 'user_id and word_id are required' USING ERRCODE = '22023';
    END IF;
    RETURN QUERY SELECT c.id, c.meaning_id, s.id, s.name, c.quote
        FROM contexts AS c
        JOIN meanings AS m ON m.user_id = c.user_id AND m.id = c.meaning_id
        JOIN sources AS s ON s.user_id = c.user_id AND s.id = c.source_id
        WHERE c.user_id = p_user_id AND m.word_id = p_word_id
        ORDER BY c.id;
END;
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION list_word_contexts(BIGINT, BIGINT);
DROP FUNCTION add_context(BIGINT, BIGINT, BIGINT, TEXT);
ALTER TABLE contexts ADD COLUMN location TEXT;

-- +goose StatementBegin
CREATE FUNCTION add_context(p_user_id BIGINT, p_meaning_id BIGINT, p_source_id BIGINT, p_quote TEXT, p_location TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
AS $$
DECLARE
    v_id BIGINT;
    v_quote TEXT := btrim(p_quote);
BEGIN
    IF p_user_id IS NULL OR p_meaning_id IS NULL OR p_source_id IS NULL
        OR v_quote IS NULL OR v_quote = '' THEN
        RAISE EXCEPTION 'user, meaning, source and nonblank quote are required' USING ERRCODE = '22023';
    END IF;
    INSERT INTO contexts(user_id, meaning_id, source_id, quote, location)
        VALUES (p_user_id, p_meaning_id, p_source_id, v_quote, NULLIF(btrim(p_location), ''))
        RETURNING id INTO v_id;
    RETURN v_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION list_word_contexts(p_user_id BIGINT, p_word_id BIGINT)
RETURNS TABLE (context_id BIGINT, meaning_id BIGINT, source_id BIGINT, source_name TEXT, quote TEXT, location TEXT)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
AS $$
BEGIN
    IF p_user_id IS NULL OR p_word_id IS NULL THEN
        RAISE EXCEPTION 'user_id and word_id are required' USING ERRCODE = '22023';
    END IF;
    RETURN QUERY SELECT c.id, c.meaning_id, s.id, s.name, c.quote, c.location
        FROM contexts AS c
        JOIN meanings AS m ON m.user_id = c.user_id AND m.id = c.meaning_id
        JOIN sources AS s ON s.user_id = c.user_id AND s.id = c.source_id
        WHERE c.user_id = p_user_id AND m.word_id = p_word_id
        ORDER BY c.id;
END;
$$;
-- +goose StatementEnd
