package query

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Query is one named query: modules/<Module>/queries/<Name>.sql.
type Query struct {
	Module, Name string
	Header
	SQL string
}

// Registry holds every named query, by module and name.
type Registry struct {
	byModule map[string]map[string]*Query
	all      []*Query
}

// moduleName is also the module's Postgres schema and its URL segment, so it
// can never start with "_" (the API's reserved /api/_health, _meta, _domain).
var moduleName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Load reads every <module>/queries/<name>.sql in fsys and checks each
// frozen-fallback against the module's other queries. It reports every
// broken file at once.
func Load(fsys fs.FS) (*Registry, error) {
	paths, err := fs.Glob(fsys, "*/queries/*.sql")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("no queries found under */queries/*.sql")
	}
	r := &Registry{byModule: map[string]map[string]*Query{}}
	var errs []error
	for _, p := range paths {
		q, err := loadOne(fsys, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		if r.byModule[q.Module] == nil {
			r.byModule[q.Module] = map[string]*Query{}
		}
		r.byModule[q.Module][q.Name] = q
		r.all = append(r.all, q)
	}
	for _, q := range r.all {
		if q.FrozenFallback == "" {
			continue
		}
		fb, ok := r.byModule[q.Module][q.FrozenFallback]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s/queries/%s.sql: frozen-fallback %q is not a query in module %q", q.Module, q.Name, q.FrozenFallback, q.Module))
		case fb.LiveOnly:
			errs = append(errs, fmt.Errorf("%s/queries/%s.sql: frozen-fallback %q is itself live-only", q.Module, q.Name, q.FrozenFallback))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	slices.SortFunc(r.all, func(a, b *Query) int {
		return cmp.Or(cmp.Compare(a.Module, b.Module), cmp.Compare(a.Name, b.Name))
	})
	return r, nil
}

func loadOne(fsys fs.FS, p string) (*Query, error) {
	module, rest, _ := strings.Cut(p, "/")
	name := strings.TrimSuffix(path.Base(rest), ".sql")
	if !moduleName.MatchString(module) {
		return nil, fmt.Errorf("module directory %q must match %s", module, moduleName)
	}
	if !queryName.MatchString(name) {
		return nil, fmt.Errorf("query name %q must match %s", name, queryName)
	}
	src, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, err
	}
	h, sql, err := ParseHeader(string(src))
	if err != nil {
		return nil, err
	}
	return &Query{Module: module, Name: name, Header: h, SQL: sql}, nil
}

// Get returns one query.
func (r *Registry) Get(module, name string) (*Query, bool) {
	q, ok := r.byModule[module][name]
	return q, ok
}

// All returns every query, sorted by module, then name.
func (r *Registry) All() []*Query {
	return slices.Clone(r.all)
}
