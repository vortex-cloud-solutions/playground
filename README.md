# playground

Public data explorers that run live on Vortex serverless PostgreSQL in the EU,
and keep working as static files after the servers stop.

Live at <https://playground.vortexcloud.eu>.

## How it works

- Each module in `modules/<name>/` ships a Postgres schema, named SQL queries,
  an ingest command, a web page and a `SOURCE.md` with its data licence.
- `playground serve` runs only those named queries. No endpoint accepts SQL;
  every parameter travels in the URL path and is checked against its declared
  type; every database session is read-only with a 5-second statement timeout.
- `playground freeze` asks the API for every answer the site can show, writes
  each one to object storage, and writes the manifest that switches the site
  to frozen mode last.

## Layout

| Path | What |
|---|---|
| `cmd/playground` | the API (`serve`) and the freeze |
| `cmd/ingest` | one ingest command per module |
| `internal/` | query registry, result encoder, database pool, HTTP server |
| `modules/` | one directory per module; `hello` is the example |
| `web/` | the React + Vite shell and each module's page |
| `deploy/` | docker-compose to run everything locally |
| `bench/` | the frozen-format benchmark and its results |

## Develop

Go 1.26 and Docker. The integration tests start PostGIS in a container through
testcontainers, so `go test ./...` needs a running Docker daemon.

## Licence

Apache-2.0: see `LICENSE`. Each module's data keeps its own licence, recorded
in that module's `SOURCE.md`.
