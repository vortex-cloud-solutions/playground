package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vortex-cloud-solutions/playground/internal/db"
	"github.com/vortex-cloud-solutions/playground/internal/pgtest"
	"github.com/vortex-cloud-solutions/playground/internal/query"
	"github.com/vortex-cloud-solutions/playground/internal/server"
	"github.com/vortex-cloud-solutions/playground/modules"
)

// The hello module, end to end: embedded queries, the guarded pool, a real
// PostGIS server with the schema applied by ingest.
func TestHelloOnARealServer(t *testing.T) {
	pg := pgtest.Start(t)
	pgtest.ApplySchema(t, pg, "hello")
	pool, err := db.Open(context.Background(), pg.APIDSN, "playground_ro")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reg, err := query.Load(modules.FS)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(reg, pool))
	defer srv.Close()

	fetch := func(path string) (int, http.Header, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, strings.TrimSpace(string(body))
	}
	table := func(body string) string {
		t.Helper()
		var env struct {
			Table json.RawMessage `json:"table"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("not an envelope: %v\n%s", err, body)
		}
		return string(env.Table)
	}

	if status, _, body := fetch("/api/_health"); status != 200 || body != "ok" {
		t.Errorf("health = %d %s", status, body)
	}

	status, hdr, body := fetch("/api/hello/greetings")
	if status != 200 || hdr.Get("Cache-Control") != "public, max-age=300" {
		t.Errorf("greetings = %d, Cache-Control %q", status, hdr.Get("Cache-Control"))
	}
	if got, want := table(body), `{"columns":["id","lang","country","word","seen_on"],"rows":6,"values":[[1,2,3,4,5,6],["nl","nl","fr","fr","de","en"],["NL","BE","BE","FR","DE","IE"],[0,0,1,1,0,2],["2026-10-01","2026-10-02","2026-10-03","2026-10-04","2026-10-05","2026-10-06"]],"dict":{"word":["hallo","bonjour","hello"]},"delta":[]}`; got != want {
		t.Errorf("greetings table\ngot  %s\nwant %s", got, want)
	}

	status, _, body = fetch("/api/hello/greeting/3")
	if got, want := table(body), `{"columns":["id","lang","country","word","seen_on"],"rows":1,"values":[[3],["fr"],["BE"],["bonjour"],["2026-10-03"]],"dict":{},"delta":[]}`; status != 200 || got != want {
		t.Errorf("greeting/3 = %d\ngot  %s\nwant %s", status, got, want)
	}

	status, _, body = fetch("/api/hello/search/HAL")
	if got, want := table(body), `{"columns":["id","word","country"],"rows":3,"values":[[1,2,5],[0,0,0],["NL","BE","DE"]],"dict":{"word":["hallo"]},"delta":[]}`; status != 200 || got != want {
		t.Errorf("search/HAL = %d\ngot  %s\nwant %s", status, got, want)
	}

	if status, _, body = fetch("/api/_domain/hello/greeting"); status != 200 || body != `{"values":[["1"],["2"],["3"],["4"],["5"],["6"]]}` {
		t.Errorf("domain = %d %s", status, body)
	}
	if status, _, body = fetch("/api/_domain/hello/greetings"); status != 200 || body != `{"values":[[]]}` {
		t.Errorf("domain of greetings = %d %s", status, body)
	}
	if status, _, _ = fetch("/api/_domain/hello/search"); status != 404 {
		t.Errorf("domain of live-only search = %d, want 404", status)
	}

	// Postgres, not the API, refuses an id beyond int4: SQLSTATE 22003.
	status, hdr, body = fetch("/api/hello/greeting/99999999999")
	if status != 400 || hdr.Get("Cache-Control") != "no-store" || !strings.Contains(body, `"code":"bad_param"`) {
		t.Errorf("greeting/99999999999 = %d %q %s, want 400 bad_param no-store", status, hdr.Get("Cache-Control"), body)
	}

	// A server that refuses the connection with a class 22 SQLSTATE is a
	// misconfiguration, not the visitor's bad parameter: statement_timeout=foo
	// in the DSN makes Postgres answer 22023 (invalid_parameter_value) during
	// connection setup, and pgx hands that PgError back inside a ConnectError.
	// A unit test cannot build that wrapper (its err field is unexported), so
	// it takes a real server.
	u, err := url.Parse(pg.APIDSN)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("statement_timeout", "foo")
	u.RawQuery = q.Encode()
	misconfigured, err := db.Open(context.Background(), u.String(), "playground_ro")
	if err != nil {
		t.Fatal(err)
	}
	defer misconfigured.Close()
	bad := httptest.NewServer(server.New(reg, misconfigured))
	defer bad.Close()
	resp, err := http.Get(bad.URL + "/api/hello/greetings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 || resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(raw), `"code":"unavailable"`) {
		t.Errorf("greetings on a misconfigured server = %d %q %s, want 503 unavailable no-store", resp.StatusCode, resp.Header.Get("Cache-Control"), raw)
	}
}
