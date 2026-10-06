package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vortex-cloud-solutions/playground/internal/pgfake"
	"github.com/vortex-cloud-solutions/playground/internal/query"
	"github.com/vortex-cloud-solutions/playground/internal/server"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

var demo = fstest.MapFS{
	"demo/queries/all.sql":   file("-- params:\n-- layout: columns\n-- max-age: 300\nSELECT n, day FROM demo.t ORDER BY n\n"),
	"demo/queries/pair.sql":  file("-- params: n:int, day:date\n-- domain: SELECT n, day FROM demo.t ORDER BY n\n-- layout: delta\n-- max-age: 3600\nSELECT n, day FROM demo.t WHERE n = $1 AND day = $2\n"),
	"demo/queries/kinds.sql": file("-- params: f:float, s:text\n-- domain: SELECT f, s FROM demo.k\n-- layout: columns\n-- max-age: 600\nSELECT $1::float8 AS f, $2::text AS s\n"),
	"demo/queries/find.sql":  file("-- params: q:text\n-- layout: columns\n-- max-age: 60\n-- live-only: true\n-- frozen-fallback: all\nSELECT n FROM demo.t WHERE s = $1\n"),
	"demo/queries/tile.sql":  file("-- layout: tiles\n-- domain: tiles z0-2 bbox -10,35,30,70\n-- max-age: 86400\nSELECT mvt FROM demo.tiles WHERE z = $1 AND x = $2 AND y = $3\n"),
}

type call struct {
	sql  string
	args []any
}

// fakeQuerier answers every query with respond and records what it was asked.
type fakeQuerier struct {
	respond func(sql string) (pgx.Rows, error)
	calls   []call
}

func (f *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.calls = append(f.calls, call{sql, args})
	return f.respond(sql)
}

func answer(rows *pgfake.Rows) func(string) (pgx.Rows, error) {
	return func(string) (pgx.Rows, error) { return rows, nil }
}

var day = func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

var col = pgfake.Col

func newServer(t *testing.T, respond func(string) (pgx.Rows, error)) (http.Handler, *fakeQuerier) {
	t.Helper()
	reg, err := query.Load(demo)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQuerier{respond: respond}
	return server.New(reg, fake), fake
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// withoutMS drops the measured "ms" from an envelope, after checking it is a
// number, and re-encodes the rest with sorted keys.
func withoutMS(t *testing.T, body string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body)
	}
	if ms, ok := m["ms"].(float64); !ok || ms < 0 {
		t.Errorf("ms = %v, want a non-negative number", m["ms"])
	}
	delete(m, "ms")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func checkHeaders(t *testing.T, rec *httptest.ResponseRecorder, status int, contentType, cacheControl string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != contentType {
		t.Errorf("Content-Type = %q, want %q", got, contentType)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControl)
	}
}

func TestHealth(t *testing.T) {
	h, fake := newServer(t, answer(pgfake.NewRows([]pgfake.Column{col("?column?", pgtype.Int4OID)}, []any{int32(1)})))
	rec := get(h, "GET", "/api/_health")
	checkHeaders(t, rec, 200, "text/plain; charset=utf-8", "no-store")
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rec.Body)
	}
	if len(fake.calls) != 1 || fake.calls[0].sql != "SELECT 1" {
		t.Errorf("calls = %+v, want one SELECT 1", fake.calls)
	}

	h, _ = newServer(t, func(string) (pgx.Rows, error) { return nil, errors.New("dial tcp: connection refused") })
	rec = get(h, "GET", "/api/_health")
	checkHeaders(t, rec, 503, "application/json", "no-store")
	if want := `{"error":{"code":"unavailable","message":"the database is unavailable"}}`; strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("body = %s, want %s", rec.Body, want)
	}
}

func TestMeta(t *testing.T) {
	h, fake := newServer(t, nil)
	rec := get(h, "GET", "/api/_meta/queries")
	checkHeaders(t, rec, 200, "application/json", "public, max-age=300")
	want := `[` +
		`{"module":"demo","name":"all","params":[],"layout":"columns","maxAge":300,"liveOnly":false,"frozenFallback":"","sql":"SELECT n, day FROM demo.t ORDER BY n"},` +
		`{"module":"demo","name":"find","params":[{"name":"q","type":"text"}],"layout":"columns","maxAge":60,"liveOnly":true,"frozenFallback":"all","sql":"SELECT n FROM demo.t WHERE s = $1"},` +
		`{"module":"demo","name":"kinds","params":[{"name":"f","type":"float"},{"name":"s","type":"text"}],"layout":"columns","maxAge":600,"liveOnly":false,"frozenFallback":"","sql":"SELECT $1::float8 AS f, $2::text AS s"},` +
		`{"module":"demo","name":"pair","params":[{"name":"n","type":"int"},{"name":"day","type":"date"}],"layout":"delta","maxAge":3600,"liveOnly":false,"frozenFallback":"","sql":"SELECT n, day FROM demo.t WHERE n = $1 AND day = $2"},` +
		`{"module":"demo","name":"tile","params":[{"name":"z","type":"int"},{"name":"x","type":"int"},{"name":"y","type":"int"}],"layout":"tiles","maxAge":86400,"liveOnly":false,"frozenFallback":"","sql":"SELECT mvt FROM demo.tiles WHERE z = $1 AND x = $2 AND y = $3"}` +
		`]`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if len(fake.calls) != 0 {
		t.Errorf("meta queried the database: %+v", fake.calls)
	}
}

func TestEnvelope(t *testing.T) {
	tests := []struct {
		name, target, cacheControl, want string
		rows                             *pgfake.Rows
		wantArgs                         []any
	}{
		{
			name:         "no params",
			target:       "/api/demo/all",
			cacheControl: "public, max-age=300",
			rows: pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID), col("day", pgtype.DateOID)},
				[]any{int32(1), day(1)}, []any{int32(2), day(2)}, []any{int32(3), day(3)}),
			wantArgs: []any{},
			want:     `{"layout":"columns","module":"demo","params":[],"query":"all","sql":"SELECT n, day FROM demo.t ORDER BY n","table":{"columns":["n","day"],"delta":[],"dict":{},"rows":3,"values":[[1,2,3],["2026-10-01","2026-10-02","2026-10-03"]]}}`,
		},
		{
			name:         "two params, delta layout",
			target:       "/api/demo/pair/42/2026-10-06",
			cacheControl: "public, max-age=3600",
			rows:         pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID), col("day", pgtype.DateOID)}, []any{int32(42), day(6)}),
			wantArgs:     []any{"42", "2026-10-06"},
			want:         `{"layout":"delta","module":"demo","params":["42","2026-10-06"],"query":"pair","sql":"SELECT n, day FROM demo.t WHERE n = $1 AND day = $2","table":{"columns":["n","day"],"delta":["n"],"dict":{},"rows":1,"values":[[42],["2026-10-06"]]}}`,
		},
		{
			name:         "escaped slash stays inside a text param",
			target:       "/api/demo/kinds/1e+21/a%2Fb",
			cacheControl: "public, max-age=600",
			rows:         pgfake.NewRows([]pgfake.Column{col("f", pgtype.Float8OID), col("s", pgtype.TextOID)}, []any{1e21, "a/b"}),
			wantArgs:     []any{"1e+21", "a/b"},
			want:         `{"layout":"columns","module":"demo","params":["1e+21","a/b"],"query":"kinds","sql":"SELECT $1::float8 AS f, $2::text AS s","table":{"columns":["f","s"],"delta":[],"dict":{},"rows":1,"values":[[1e+21],["a/b"]]}}`,
		},
		{
			name:         "a live-only query is served live",
			target:       "/api/demo/find/hal'lo%20OR%201=1",
			cacheControl: "public, max-age=60",
			rows:         pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID)}),
			wantArgs:     []any{"hal'lo OR 1=1"},
			want:         `{"layout":"columns","module":"demo","params":["hal'lo OR 1=1"],"query":"find","sql":"SELECT n FROM demo.t WHERE s = $1","table":{"columns":["n"],"delta":[],"dict":{},"rows":0,"values":[[]]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, fake := newServer(t, answer(tt.rows))
			rec := get(h, "GET", tt.target)
			checkHeaders(t, rec, 200, "application/json", tt.cacheControl)
			if got := withoutMS(t, rec.Body.String()); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
			if len(fake.calls) != 1 || !reflect.DeepEqual(fake.calls[0].args, tt.wantArgs) {
				t.Errorf("calls = %+v, want one with args %q", fake.calls, tt.wantArgs)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	ok := answer(pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID)}, []any{int32(1)}))
	fails := func(err error) func(string) (pgx.Rows, error) {
		return func(string) (pgx.Rows, error) { return pgfake.ErrRows(err), nil }
	}
	tests := []struct {
		name, method, target string
		respond              func(string) (pgx.Rows, error)
		status               int
		code                 string
	}{
		{"int with letters", "GET", "/api/demo/pair/4x/2026-10-06", ok, 400, "bad_param"},
		{"int with a leading zero", "GET", "/api/demo/pair/042/2026-10-06", ok, 400, "bad_param"},
		{"int with a plus sign", "GET", "/api/demo/pair/+42/2026-10-06", ok, 400, "bad_param"},
		{"impossible date", "GET", "/api/demo/pair/42/2026-13-01", ok, 400, "bad_param"},
		{"float not in canonical form", "GET", "/api/demo/kinds/1.50/x", ok, 400, "bad_param"},
		{"float NaN", "GET", "/api/demo/kinds/NaN/x", ok, 400, "bad_param"},
		{"text with NUL", "GET", "/api/demo/kinds/1.5/a%00b", ok, 400, "bad_param"},
		{"text that is a dot-dot segment", "GET", "/api/demo/kinds/1.5/%2E%2E", ok, 400, "bad_param"},
		{"text over 256 bytes", "GET", "/api/demo/kinds/1.5/" + strings.Repeat("a", 257), ok, 400, "bad_param"},
		{"unknown module", "GET", "/api/nope/all", ok, 404, "not_found"},
		{"unknown query", "GET", "/api/demo/nope", ok, 404, "not_found"},
		{"too few params", "GET", "/api/demo/pair/42", ok, 404, "not_found"},
		{"too many params", "GET", "/api/demo/all/extra", ok, 404, "not_found"},
		{"trailing slash", "GET", "/api/demo/all/", ok, 404, "not_found"},
		{"tiles query on the JSON route", "GET", "/api/demo/tile/1/2/3", ok, 404, "not_found"},
		{"domain of a live-only query", "GET", "/api/_domain/demo/find", ok, 404, "not_found"},
		{"domain of a tiles query", "GET", "/api/_domain/demo/tile", ok, 404, "not_found"},
		{"reserved prefix", "GET", "/api/_other/x", ok, 404, "not_found"},
		{"bare /api/", "GET", "/api/", ok, 404, "not_found"},
		{"outside /api", "GET", "/healthz", ok, 404, "not_found"},
		{"POST", "POST", "/api/demo/all", ok, 404, "not_found"},
		{"statement timeout", "GET", "/api/demo/all", fails(&pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}), 504, "timeout"},
		{"int4 overflow from Postgres", "GET", "/api/demo/pair/99999999999/2026-10-06", fails(&pgconn.PgError{Code: "22003", Message: `value "99999999999" is out of range for type integer`}), 400, "bad_param"},
		{"missing table", "GET", "/api/demo/all", fails(&pgconn.PgError{Code: "42P01", Message: `relation "demo.t" does not exist`}), 503, "unavailable"},
		{"connection refused", "GET", "/api/demo/all", func(string) (pgx.Rows, error) { return nil, errors.New("dial tcp: connection refused") }, 503, "unavailable"},
		{"unencodable column", "GET", "/api/demo/all", answer(pgfake.NewRows([]pgfake.Column{col("eur", pgtype.NumericOID)})), 503, "unavailable"},
		{"domain returning a NULL", "GET", "/api/_domain/demo/pair", answer(pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID), col("day", pgtype.DateOID)}, []any{nil, day(1)})), 503, "unavailable"},
		{"domain with the wrong column count", "GET", "/api/_domain/demo/pair", answer(pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID)}, []any{int32(1)})), 503, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newServer(t, tt.respond)
			rec := get(h, tt.method, tt.target)
			checkHeaders(t, rec, tt.status, "application/json", "no-store")
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v\n%s", err, rec.Body)
			}
			if body.Error.Code != tt.code || body.Error.Message == "" {
				t.Errorf("error = %+v, want code %s with a message", body.Error, tt.code)
			}
		})
	}
}

func TestDomain(t *testing.T) {
	h, fake := newServer(t, answer(pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID), col("day", pgtype.DateOID)},
		[]any{int32(1), day(1)}, []any{int32(-2), day(2)})))
	rec := get(h, "GET", "/api/_domain/demo/pair")
	checkHeaders(t, rec, 200, "application/json", "no-store")
	if want := `{"values":[["1","2026-10-01"],["-2","2026-10-02"]]}`; strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("got  %s\nwant %s", rec.Body, want)
	}
	if len(fake.calls) != 1 || fake.calls[0].sql != "SELECT n, day FROM demo.t ORDER BY n" || len(fake.calls[0].args) != 0 {
		t.Errorf("calls = %+v, want the domain SQL with no args", fake.calls)
	}

	h, fake = newServer(t, answer(pgfake.NewRows([]pgfake.Column{col("f", pgtype.Float4OID), col("s", pgtype.TextOID)},
		[]any{float32(0.1), "a/b"})))
	rec = get(h, "GET", "/api/_domain/demo/kinds")
	if want := `{"values":[["0.1","a/b"]]}`; strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("got  %s\nwant %s", rec.Body, want)
	}

	h, fake = newServer(t, nil)
	rec = get(h, "GET", "/api/_domain/demo/all")
	checkHeaders(t, rec, 200, "application/json", "no-store")
	if want := `{"values":[[]]}`; strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("a query without params: got %s, want %s", rec.Body, want)
	}
	if len(fake.calls) != 0 {
		t.Errorf("a query without params queried the database: %+v", fake.calls)
	}
}
