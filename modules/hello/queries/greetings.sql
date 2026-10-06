-- params:
-- layout: columns
-- max-age: 300
-- live-only: false
-- frozen-fallback:
SELECT id, lang, country, word, seen_on
FROM hello.greetings
ORDER BY id
