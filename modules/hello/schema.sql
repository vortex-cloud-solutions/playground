CREATE SCHEMA IF NOT EXISTS hello;
GRANT USAGE ON SCHEMA hello TO playground_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA hello GRANT SELECT ON TABLES TO playground_ro;

-- The example module: six hand-written rows, so the API, the freeze and the
-- web shell can be exercised without any ingest source.
CREATE TABLE IF NOT EXISTS hello.greetings (
    id      integer PRIMARY KEY,
    lang    text    NOT NULL,
    country text    NOT NULL,
    word    text    NOT NULL,
    seen_on date    NOT NULL
);

INSERT INTO hello.greetings (id, lang, country, word, seen_on) VALUES
    (1, 'nl', 'NL', 'hallo',   '2026-10-01'),
    (2, 'nl', 'BE', 'hallo',   '2026-10-02'),
    (3, 'fr', 'BE', 'bonjour', '2026-10-03'),
    (4, 'fr', 'FR', 'bonjour', '2026-10-04'),
    (5, 'de', 'DE', 'hallo',   '2026-10-05'),
    (6, 'en', 'IE', 'hello',   '2026-10-06')
ON CONFLICT (id) DO NOTHING;
