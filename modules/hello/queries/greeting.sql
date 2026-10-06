-- params: id:int
-- domain: SELECT id FROM hello.greetings ORDER BY id
-- layout: columns
-- max-age: 3600
-- live-only: false
-- frozen-fallback:
SELECT id, lang, country, word, seen_on
FROM hello.greetings
WHERE id = $1
