
-- +goose Up

CREATE TABLE users (
    id BIGINT PRIMARY KEY
);

CREATE TABLE words (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    text TEXT NOT NULL CHECK (btrim(text) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT words_user_id_unique UNIQUE(user_id, id)
);
CREATE UNIQUE INDEX words_user_text_unique ON words (user_id, lower(text));

CREATE TABLE meanings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    word_id BIGINT NOT NULL,
    definition TEXT NOT NULL CHECK (btrim(definition) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT meanings_user_id_unique UNIQUE(user_id, id),

    CONSTRAINT meanings_words_fk FOREIGN KEY (user_id, word_id) REFERENCES words(user_id, id) ON DELETE CASCADE
);
CREATE INDEX meanings_user_word_idx ON meanings (user_id, word_id);

CREATE TABLE sources (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT sources_user_id_unique UNIQUE (user_id, id)
);
CREATE UNIQUE INDEX sources_user_name_unique
    ON sources (user_id, lower(name));

CREATE TABLE contexts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    meaning_id BIGINT NOT NULL,
    source_id BIGINT NOT NULL,
    quote TEXT,
    location TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT contexts_meaning_fk
        FOREIGN KEY (user_id, meaning_id)
        REFERENCES meanings (user_id, id)
        ON DELETE CASCADE,

    CONSTRAINT contexts_source_fk
        FOREIGN KEY (user_id, source_id)
        REFERENCES sources (user_id, id)
        ON DELETE CASCADE
);

CREATE INDEX contexts_user_meaning_idx
    ON contexts (user_id, meaning_id);

CREATE INDEX contexts_user_source_idx
    ON contexts (user_id, source_id);

-- +goose Down

DROP TABLE contexts;
DROP TABLE sources;
DROP TABLE meanings;
DROP TABLE words;
DROP TABLE users;
