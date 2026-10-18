-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION add_source(p_user_id BIGINT, p_name TEXT)
RETURNS TABLE (source_id BIGINT, name TEXT, source_created BOOLEAN)
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
AS $$
DECLARE
    v_name TEXT := btrim(p_name);
    v_id BIGINT;
    v_created BOOLEAN;
BEGIN
    IF p_user_id IS NULL OR v_name IS NULL OR v_name = '' THEN
        RAISE EXCEPTION 'user_id and nonblank name are required' USING ERRCODE = '22023';
    END IF;
    INSERT INTO users(id) VALUES (p_user_id) ON CONFLICT DO NOTHING;
    INSERT INTO sources(user_id, name) VALUES (p_user_id, v_name)
        ON CONFLICT DO NOTHING RETURNING sources.id INTO v_id;
    v_created := FOUND;
    IF NOT v_created THEN
        SELECT s.id, s.name INTO v_id, v_name FROM sources AS s
            WHERE s.user_id = p_user_id AND lower(s.name) = lower(v_name);
    END IF;
    RETURN QUERY SELECT v_id, v_name, v_created;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION list_sources(p_user_id BIGINT, p_limit INTEGER, p_offset INTEGER)
RETURNS TABLE (source_id BIGINT, name TEXT)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
AS $$
BEGIN
    IF p_user_id IS NULL OR p_limit IS NULL OR p_limit <= 0
        OR p_offset IS NULL OR p_offset < 0 THEN
        RAISE EXCEPTION 'user_id is required; limit must be positive; offset must be nonnegative'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY SELECT s.id, s.name FROM sources AS s
        WHERE s.user_id = p_user_id ORDER BY lower(s.name), s.id LIMIT p_limit OFFSET p_offset;
END;
$$;
-- +goose StatementEnd

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

-- +goose Down

DROP FUNCTION list_word_contexts(BIGINT, BIGINT);
DROP FUNCTION add_context(BIGINT, BIGINT, BIGINT, TEXT, TEXT);
DROP FUNCTION list_sources(BIGINT, INTEGER, INTEGER);
DROP FUNCTION add_source(BIGINT, TEXT);
