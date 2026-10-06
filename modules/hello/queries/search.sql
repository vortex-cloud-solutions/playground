-- params: q:text
-- layout: columns
-- max-age: 60
-- live-only: true
-- frozen-fallback: greetings
SELECT id, word, country
FROM hello.greetings
WHERE strpos(lower(word), lower($1)) > 0
ORDER BY id
