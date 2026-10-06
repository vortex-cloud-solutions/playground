// Package server is the named-query HTTP API. It runs only the queries in
// the registry; no endpoint accepts SQL, and every parameter travels in the
// URL path.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vortex-cloud-solutions/playground/internal/encode"
	"github.com/vortex-cloud-solutions/playground/internal/query"
)

// Querier is the part of *pgxpool.Pool the server uses.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// requestTimeout bounds a request end to end, including waiting for a pool
// connection while a suspended compute wakes; statement_timeout (5 s) bounds
// the query itself.
const requestTimeout = 30 * time.Second

type server struct {
	reg  *query.Registry
	pool Querier
	meta []byte
}

type metaQuery struct {
	Module         string        `json:"module"`
	Name           string        `json:"name"`
	Params         []query.Param `json:"params"`
	Layout         query.Layout  `json:"layout"`
	MaxAge         int           `json:"maxAge"`
	LiveOnly       bool          `json:"liveOnly"`
	FrozenFallback string        `json:"frozenFallback"`
	SQL            string        `json:"sql"`
}

type envelope struct {
	Module string       `json:"module"`
	Query  string       `json:"query"`
	Params []string     `json:"params"`
	SQL    string       `json:"sql"`
	MS     float64      `json:"ms"`
	Layout query.Layout `json:"layout"`
	Table  encode.Table `json:"table"`
}

// apiError is a failure with the status and code the client sees.
type apiError struct {
	status  int
	code    string
	message string
}

func notFound(format string, args ...any) *apiError {
	return &apiError{http.StatusNotFound, "not_found", fmt.Sprintf(format, args...)}
}

// New returns the API handler.
func New(reg *query.Registry, pool Querier) http.Handler {
	all := reg.All()
	meta := make([]metaQuery, len(all))
	for i, q := range all {
		meta[i] = metaQuery{q.Module, q.Name, q.Params, q.Layout, q.MaxAge, q.LiveOnly, q.FrozenFallback, q.SQL}
	}
	return &server{reg: reg, pool: pool, meta: mustJSON(meta)}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, r, notFound("only GET and HEAD are served"))
		return
	}
	segs, ok := splitPath(r.URL.EscapedPath())
	if !ok {
		writeError(w, r, notFound("no route for %s", r.URL.EscapedPath()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	switch {
	case len(segs) == 1 && segs[0] == "_health":
		s.health(ctx, w, r)
	case len(segs) == 2 && segs[0] == "_meta" && segs[1] == "queries":
		w.Header().Set("Cache-Control", "public, max-age=300")
		writeBody(w, "application/json", s.meta)
	case len(segs) == 3 && segs[0] == "_domain":
		s.domain(ctx, w, r, segs[1], segs[2])
	case len(segs) >= 2 && !strings.HasPrefix(segs[0], "_"):
		s.run(ctx, w, r, segs[0], segs[1], segs[2:])
	default:
		writeError(w, r, notFound("no route for %s", r.URL.EscapedPath()))
	}
}

// splitPath returns the unescaped segments after /api/. Splitting before
// unescaping keeps %2F inside a text parameter. Empty segments are refused.
func splitPath(escaped string) ([]string, bool) {
	rest, ok := strings.CutPrefix(escaped, "/api/")
	if !ok || rest == "" {
		return nil, false
	}
	segs := strings.Split(rest, "/")
	for i, seg := range segs {
		u, err := url.PathUnescape(seg)
		if err != nil || u == "" {
			return nil, false
		}
		segs[i] = u
	}
	return segs, true
}

func (s *server) health(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(ctx, "SELECT 1")
	if err == nil {
		for rows.Next() {
		}
		rows.Close()
		err = rows.Err()
	}
	if err != nil {
		writeError(w, r, &apiError{http.StatusServiceUnavailable, "unavailable", "the database is unavailable"}, "err", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeBody(w, "text/plain; charset=utf-8", []byte("ok"))
}

func (s *server) lookup(module, name string) (*query.Query, *apiError) {
	q, ok := s.reg.Get(module, name)
	if !ok {
		return nil, notFound("no query %s/%s", module, name)
	}
	if q.Layout == query.LayoutTiles {
		return nil, notFound("%s/%s is a tiles query, served as /{z}/{x}/{y}.mvt", module, name)
	}
	return q, nil
}

func (s *server) domain(ctx context.Context, w http.ResponseWriter, r *http.Request, module, name string) {
	q, apiErr := s.lookup(module, name)
	if apiErr != nil {
		writeError(w, r, apiErr)
		return
	}
	if q.LiveOnly {
		writeError(w, r, notFound("%s/%s is live-only and has no domain", module, name))
		return
	}
	values := [][]string{}
	if len(q.Params) == 0 {
		values = append(values, []string{})
	} else {
		var err error
		if values, err = s.domainValues(ctx, q); err != nil {
			writeError(w, r, classify(err), "module", module, "query", name, "err", err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeBody(w, "application/json", mustJSON(map[string][][]string{"values": values}))
}

func (s *server) domainValues(ctx context.Context, q *query.Query) ([][]string, error) {
	rows, err := s.pool.Query(ctx, q.Domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if n := len(rows.FieldDescriptions()); n != len(q.Params) {
		return nil, fmt.Errorf("domain returns %d columns for %d params", n, len(q.Params))
	}
	values := [][]string{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		tuple := make([]string, len(vals))
		for i, v := range vals {
			if tuple[i], err = formatDomainValue(q.Params[i].Type, v); err != nil {
				return nil, err
			}
		}
		values = append(values, tuple)
	}
	return values, rows.Err()
}

func (s *server) run(ctx context.Context, w http.ResponseWriter, r *http.Request, module, name string, params []string) {
	q, apiErr := s.lookup(module, name)
	if apiErr != nil {
		writeError(w, r, apiErr)
		return
	}
	if len(params) != len(q.Params) {
		writeError(w, r, notFound("%s/%s takes %d params, got %d", module, name, len(q.Params), len(params)))
		return
	}
	args := make([]any, len(params))
	for i, p := range params {
		if err := checkParam(q.Params[i].Type, p); err != nil {
			writeError(w, r, &apiError{http.StatusBadRequest, "bad_param", fmt.Sprintf("param %s: %v", q.Params[i].Name, err)})
			return
		}
		// pgx sends a Go string in text format for any parameter type, so
		// Postgres parses it as the type it infers for $n.
		args[i] = p
	}
	start := time.Now()
	rows, err := s.pool.Query(ctx, q.SQL, args...)
	var table encode.Table
	if err == nil {
		table, err = encode.Encode(rows, q.Layout)
	} else if rows != nil {
		rows.Close()
	}
	if err != nil {
		writeError(w, r, classify(err), "module", module, "query", name, "err", err)
		return
	}
	ms := math.Round(float64(time.Since(start).Microseconds())/100) / 10
	body, err := encodeJSON(envelope{module, name, params, q.SQL, ms, q.Layout, table})
	if err != nil {
		writeError(w, r, &apiError{http.StatusServiceUnavailable, "unavailable", "the result could not be encoded"}, "module", module, "query", name, "err", err)
		return
	}
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", q.MaxAge))
	writeBody(w, "application/json", body)
}

// classify maps a query failure to what the client sees: SQLSTATE 57014
// (statement_timeout) is a timeout, class 22 (data exception: a parameter
// Postgres could not take, such as an int4 overflow) is a bad parameter,
// and anything else is the database being unavailable.
func classify(err error) *apiError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "57014":
			return &apiError{http.StatusGatewayTimeout, "timeout", "the query ran past the 5 s statement timeout"}
		case strings.HasPrefix(pgErr.Code, "22"):
			return &apiError{http.StatusBadRequest, "bad_param", pgErr.Message}
		}
	}
	return &apiError{http.StatusServiceUnavailable, "unavailable", "the database is unavailable"}
}

func writeError(w http.ResponseWriter, r *http.Request, e *apiError, logArgs ...any) {
	if e.status >= 500 {
		slog.Error("request failed", append([]any{"path", r.URL.EscapedPath(), "code", e.code}, logArgs...)...)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_, _ = w.Write(mustJSON(map[string]map[string]string{"error": {"code": e.code, "message": e.message}}))
}

func writeBody(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}

// encodeJSON encodes v without HTML escaping, so SQL reads as written in
// `curl | jq` and in the frozen objects. The body ends in a newline.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// mustJSON is encodeJSON for values built only from strings, ints and bools,
// which cannot fail to encode.
func mustJSON(v any) []byte {
	b, err := encodeJSON(v)
	if err != nil {
		panic(fmt.Sprintf("server: encode %T: %v", v, err))
	}
	return b
}
