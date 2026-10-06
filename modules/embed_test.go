package modules_test

import (
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/vortex-cloud-solutions/playground/internal/query"
	"github.com/vortex-cloud-solutions/playground/modules"
)

func TestHelloQueries(t *testing.T) {
	reg, err := query.Load(modules.FS)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]query.Header{
		"greetings": {Params: []query.Param{}, Layout: query.LayoutColumns, MaxAge: 300},
		"greeting": {
			Params: []query.Param{{Name: "id", Type: query.ParamInt}},
			Domain: "SELECT id FROM hello.greetings ORDER BY id", Layout: query.LayoutColumns, MaxAge: 3600,
		},
		"search": {
			Params: []query.Param{{Name: "q", Type: query.ParamText}},
			Layout: query.LayoutColumns, MaxAge: 60, LiveOnly: true, FrozenFallback: "greetings",
		},
	}
	for name, h := range want {
		q, ok := reg.Get("hello", name)
		if !ok {
			t.Errorf("hello/%s is missing", name)
			continue
		}
		if !reflect.DeepEqual(q.Header, h) {
			t.Errorf("hello/%s header = %+v, want %+v", name, q.Header, h)
		}
	}
}

// Every module with queries has a schema.sql that starts with the shared
// prologue for its own schema, so `ingest <module>` can apply it first.
func TestEverySchemaStartsWithThePrologue(t *testing.T) {
	queries, err := fs.Glob(modules.FS, "*/queries/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, q := range queries {
		m := strings.SplitN(q, "/", 2)[0]
		if seen[m] {
			continue
		}
		seen[m] = true
		src, err := fs.ReadFile(modules.FS, path.Join(m, "schema.sql"))
		if err != nil {
			t.Errorf("module %s: %v", m, err)
			continue
		}
		prologue := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %[1]s;\n"+
			"GRANT USAGE ON SCHEMA %[1]s TO playground_ro;\n"+
			"ALTER DEFAULT PRIVILEGES IN SCHEMA %[1]s GRANT SELECT ON TABLES TO playground_ro;\n", m)
		if !strings.HasPrefix(string(src), prologue) {
			t.Errorf("%s/schema.sql does not start with the prologue:\n%s", m, prologue)
		}
	}
	if !seen["hello"] {
		t.Error("module hello is not embedded")
	}
}
