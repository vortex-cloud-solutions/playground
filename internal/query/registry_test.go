package query_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vortex-cloud-solutions/playground/internal/query"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

const (
	plain    = "-- params:\n-- layout: columns\n-- max-age: 300\nSELECT 1\n"
	withID   = "-- params: id:int\n-- domain: SELECT id FROM t\n-- layout: columns\n-- max-age: 300\nSELECT $1\n"
	liveOnly = "-- params: q:text\n-- layout: columns\n-- max-age: 60\n-- live-only: true\nSELECT $1\n"
)

func fallbackTo(name string) string {
	return "-- params: q:text\n-- layout: columns\n-- max-age: 60\n-- live-only: true\n-- frozen-fallback: " + name + "\nSELECT $1\n"
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"ted/queries/suppliers.sql":   file(plain),
		"ted/queries/search.sql":      file(fallbackTo("suppliers")),
		"meps/queries/vote.sql":       file(withID),
		"meps/queries/vote-index.sql": file(plain),
		"meps/schema.sql":             file("CREATE SCHEMA IF NOT EXISTS meps;"),
		"meps/queries/README.md":      file("not a query"),
	}
	reg, err := query.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, q := range reg.All() {
		got = append(got, q.Module+"/"+q.Name)
	}
	want := "meps/vote meps/vote-index ted/search ted/suppliers"
	if strings.Join(got, " ") != want {
		t.Errorf("All() = %v, want %s", got, want)
	}
	q, ok := reg.Get("ted", "search")
	if !ok || !q.LiveOnly || q.FrozenFallback != "suppliers" || q.SQL != "SELECT $1" {
		t.Errorf("Get(ted, search) = %+v, %v", q, ok)
	}
	if _, ok := reg.Get("ted", "vote"); ok {
		t.Error("Get(ted, vote) found a query from another module")
	}
	if _, ok := reg.Get("nope", "vote"); ok {
		t.Error("Get(nope, vote) found a query in a module that does not exist")
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr []string
	}{
		{
			name:    "no queries at all",
			fsys:    fstest.MapFS{"meps/schema.sql": file("CREATE SCHEMA meps;")},
			wantErr: []string{"no queries found"},
		},
		{
			name:    "fallback that does not exist",
			fsys:    fstest.MapFS{"ted/queries/search.sql": file(fallbackTo("suppliers"))},
			wantErr: []string{`ted/queries/search.sql: frozen-fallback "suppliers" is not a query in module "ted"`},
		},
		{
			name: "fallback in another module",
			fsys: fstest.MapFS{
				"ted/queries/search.sql":     file(fallbackTo("suppliers")),
				"meps/queries/suppliers.sql": file(plain),
			},
			wantErr: []string{`frozen-fallback "suppliers" is not a query in module "ted"`},
		},
		{
			name: "fallback that is itself live-only",
			fsys: fstest.MapFS{
				"ted/queries/search.sql":    file(fallbackTo("suppliers")),
				"ted/queries/suppliers.sql": file(liveOnly),
			},
			wantErr: []string{`frozen-fallback "suppliers" is itself live-only`},
		},
		{
			name:    "module directory that is not a schema name",
			fsys:    fstest.MapFS{"_meta/queries/x.sql": file(plain)},
			wantErr: []string{`module directory "_meta" must match`},
		},
		{
			name:    "query file name with a capital",
			fsys:    fstest.MapFS{"meps/queries/Vote.sql": file(plain)},
			wantErr: []string{`query name "Vote" must match`},
		},
		{
			name: "every broken file is reported, with its path",
			fsys: fstest.MapFS{
				"meps/queries/a.sql": file("-- params:\n-- layout: rows\n-- max-age: 5\nSELECT 1"),
				"meps/queries/b.sql": file("-- params:\n-- layout: columns\nSELECT 1"),
			},
			wantErr: []string{`meps/queries/a.sql: layout "rows"`, `meps/queries/b.sql: missing header key "max-age"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := query.Load(tt.fsys)
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			for _, w := range tt.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}
}
