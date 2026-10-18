-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION list_words(p_user_id BIGINT, p_limit INTEGER, p_offset INTEGER)
RETURNS TABLE (word_id BIGINT, word TEXT)
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

    RETURN QUERY
        SELECT w.id, w.text FROM words AS w
        WHERE w.user_id = p_user_id
        ORDER BY lower(w.text), w.id
        LIMIT p_limit OFFSET p_offset;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION list_word_meanings(p_user_id BIGINT, p_word_id BIGINT)
RETURNS TABLE (meaning_id BIGINT, definition TEXT, synonyms TEXT[])
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
AS $$
BEGIN
    IF p_user_id IS NULL OR p_word_id IS NULL THEN
        RAISE EXCEPTION 'user_id and word_id are required' USING ERRCODE = '22023';
    END IF;

    RETURN QUERY
        SELECT m.id, m.definition, m.synonyms FROM meanings AS m
        WHERE m.user_id = p_user_id AND m.word_id = p_word_id
        ORDER BY m.id;
END;
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION list_word_meanings(BIGINT, BIGINT);
DROP FUNCTION list_words(BIGINT, INTEGER, INTEGER);
