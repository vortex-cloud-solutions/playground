package query_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vortex-cloud-solutions/playground/internal/query"
)

func TestParseHeader(t *testing.T) {
	tiles := []query.Param{{Name: "z", Type: query.ParamInt}, {Name: "x", Type: query.ParamInt}, {Name: "y", Type: query.ParamInt}}
	tests := []struct {
		name    string
		src     string
		want    query.Header
		wantSQL string
		wantErr string
	}{
		{
			name: "every key, one param",
			src: "-- params: id:int\n-- domain: SELECT id FROM meps.votes ORDER BY id\n-- layout: columns\n" +
				"-- max-age: 3600\n-- live-only: false\n-- frozen-fallback:\nSELECT * FROM meps.votes WHERE id = $1\n",
			want: query.Header{
				Params: []query.Param{{Name: "id", Type: query.ParamInt}},
				Domain: "SELECT id FROM meps.votes ORDER BY id", Layout: query.LayoutColumns, MaxAge: 3600,
			},
			wantSQL: "SELECT * FROM meps.votes WHERE id = $1",
		},
		{
			name:    "no params, no domain",
			src:     "-- params:\n-- layout: columns\n-- max-age: 300\nSELECT 1\n",
			want:    query.Header{Params: []query.Param{}, Layout: query.LayoutColumns, MaxAge: 300},
			wantSQL: "SELECT 1",
		},
		{
			name: "every param type, delta layout",
			src: "-- params: n:int, s:text, d:date, f:float\n-- domain: SELECT n, s, d, f FROM t\n" +
				"-- layout: delta\n-- max-age: 0\nSELECT $1, $2, $3, $4",
			want: query.Header{
				Params: []query.Param{
					{Name: "n", Type: query.ParamInt}, {Name: "s", Type: query.ParamText},
					{Name: "d", Type: query.ParamDate}, {Name: "f", Type: query.ParamFloat},
				},
				Domain: "SELECT n, s, d, f FROM t", Layout: query.LayoutDelta,
			},
			wantSQL: "SELECT $1, $2, $3, $4",
		},
		{
			name:    "tiles implies z, x, y",
			src:     "-- layout: tiles\n-- domain: tiles z4-7 bbox -25,34,45,72\n-- max-age: 86400\nSELECT mvt($1, $2, $3)",
			want:    query.Header{Params: tiles, Domain: "tiles z4-7 bbox -25,34,45,72", Layout: query.LayoutTiles, MaxAge: 86400},
			wantSQL: "SELECT mvt($1, $2, $3)",
		},
		{
			name:    "tiles may declare exactly z, x, y",
			src:     "-- params: z:int,x:int,y:int\n-- layout: tiles\n-- domain: tiles z0-2 bbox -10.5,35.25,30,70\n-- max-age: 60\nSELECT 1",
			want:    query.Header{Params: tiles, Domain: "tiles z0-2 bbox -10.5,35.25,30,70", Layout: query.LayoutTiles, MaxAge: 60},
			wantSQL: "SELECT 1",
		},
		{
			name:    "live-only needs no domain and may name a fallback",
			src:     "-- params: q:text\n-- layout: columns\n-- max-age: 60\n-- live-only: true\n-- frozen-fallback: vote-index\nSELECT 1",
			want:    query.Header{Params: []query.Param{{Name: "q", Type: query.ParamText}}, Layout: query.LayoutColumns, MaxAge: 60, LiveOnly: true, FrozenFallback: "vote-index"},
			wantSQL: "SELECT 1",
		},
		{
			name:    "live-only tiles needs no domain",
			src:     "-- layout: tiles\n-- max-age: 60\n-- live-only: true\nSELECT 1",
			want:    query.Header{Params: tiles, Layout: query.LayoutTiles, MaxAge: 60, LiveOnly: true},
			wantSQL: "SELECT 1",
		},
		{
			name:    "CRLF line endings",
			src:     "-- params:\r\n-- layout: columns\r\n-- max-age: 5\r\nSELECT 1\r\n",
			want:    query.Header{Params: []query.Param{}, Layout: query.LayoutColumns, MaxAge: 5},
			wantSQL: "SELECT 1",
		},
		{
			name:    "a comment after a blank line belongs to the SQL",
			src:     "-- params:\n-- layout: columns\n-- max-age: 5\n\n-- totals per country\nSELECT 1\n",
			want:    query.Header{Params: []query.Param{}, Layout: query.LayoutColumns, MaxAge: 5},
			wantSQL: "-- totals per country\nSELECT 1",
		},
		{name: "unknown key", src: "-- params:\n-- cache: 5\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `line 2: unknown header key "cache"`},
		{name: "duplicate key", src: "-- params:\n-- params:\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `line 2: duplicate header key "params"`},
		{name: "malformed header line", src: "-- params:\n-- votes per country\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: "line 2: header lines read `-- key: value`"},
		{name: "no SQL", src: "-- params:\n-- layout: columns\n-- max-age: 5\n\n", wantErr: "no SQL after the header"},
		{name: "missing layout", src: "-- params:\n-- max-age: 5\nSELECT 1", wantErr: `missing header key "layout"`},
		{name: "unknown layout", src: "-- params:\n-- layout: rows\n-- max-age: 5\nSELECT 1", wantErr: `layout "rows": want columns, delta or tiles`},
		{name: "missing params", src: "-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `missing header key "params"`},
		{name: "param without type", src: "-- params: id\n-- domain: SELECT 1\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `param "id": want name:type`},
		{name: "unknown param type", src: "-- params: id:uuid\n-- domain: SELECT 1\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `param "id": type "uuid": want int, text, date or float`},
		{name: "bad param name", src: "-- params: Id:int\n-- domain: SELECT 1\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `param "Id": name must match`},
		{name: "param declared twice", src: "-- params: id:int, id:text\n-- domain: SELECT 1, 2\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: `param "id" declared twice`},
		{name: "missing max-age", src: "-- params:\n-- layout: columns\nSELECT 1", wantErr: `missing header key "max-age"`},
		{name: "max-age not a number", src: "-- params:\n-- layout: columns\n-- max-age: 1h\nSELECT 1", wantErr: `max-age "1h": want whole seconds from 0 to 31536000`},
		{name: "max-age negative", src: "-- params:\n-- layout: columns\n-- max-age: -1\nSELECT 1", wantErr: `max-age "-1"`},
		{name: "max-age over a year", src: "-- params:\n-- layout: columns\n-- max-age: 31536001\nSELECT 1", wantErr: `max-age "31536001"`},
		{name: "live-only not a bool", src: "-- params:\n-- layout: columns\n-- max-age: 5\n-- live-only: yes\nSELECT 1", wantErr: `live-only "yes": want true or false`},
		{name: "params without domain", src: "-- params: id:int\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: "domain is required unless live-only: true"},
		{name: "domain on a query without params", src: "-- params:\n-- domain: SELECT 1\n-- layout: columns\n-- max-age: 5\nSELECT 1", wantErr: "domain must be empty on a query without params"},
		{name: "domain on a live-only query without params", src: "-- params:\n-- domain: SELECT 1\n-- layout: columns\n-- max-age: 5\n-- live-only: true\nSELECT 1", wantErr: "domain must be empty on a query without params"},
		{name: "live-only tiles with a junk domain", src: "-- layout: tiles\n-- domain: junk\n-- max-age: 5\n-- live-only: true\nSELECT 1", wantErr: "a tiles query wants"},
		{name: "frozen-fallback on a query that is not live-only", src: "-- params:\n-- layout: columns\n-- max-age: 5\n-- frozen-fallback: other\nSELECT 1", wantErr: "frozen-fallback is only allowed on a live-only query"},
		{name: "frozen-fallback with live-only false", src: "-- params:\n-- layout: columns\n-- max-age: 5\n-- live-only: false\n-- frozen-fallback: other\nSELECT 1", wantErr: "frozen-fallback is only allowed on a live-only query"},
		{name: "frozen-fallback not a query name", src: "-- params: q:text\n-- layout: columns\n-- max-age: 5\n-- live-only: true\n-- frozen-fallback: Other.sql\nSELECT 1", wantErr: `frozen-fallback "Other.sql" is not a query name`},
		{name: "tiles with other params", src: "-- params: z:int\n-- layout: tiles\n-- domain: tiles z0-2 bbox -10,35,30,70\n-- max-age: 5\nSELECT 1", wantErr: "layout tiles implies params z:int,x:int,y:int"},
		{name: "tiles with empty params", src: "-- params:\n-- layout: tiles\n-- domain: tiles z0-2 bbox -10,35,30,70\n-- max-age: 5\nSELECT 1", wantErr: "layout tiles implies params z:int,x:int,y:int"},
		{name: "tiles with an SQL domain", src: "-- layout: tiles\n-- domain: SELECT z, x, y FROM t\n-- max-age: 5\nSELECT 1", wantErr: "a tiles query wants `tiles z<min>-<max> bbox <w>,<s>,<e>,<n>`"},
		{name: "tiles without domain", src: "-- layout: tiles\n-- max-age: 5\nSELECT 1", wantErr: "a tiles query wants"},
		{name: "tiles zoom range inverted", src: "-- layout: tiles\n-- domain: tiles z7-4 bbox -10,35,30,70\n-- max-age: 5\nSELECT 1", wantErr: "want 0 <= min <= max <= 22"},
		{name: "tiles zoom past 22", src: "-- layout: tiles\n-- domain: tiles z4-23 bbox -10,35,30,70\n-- max-age: 5\nSELECT 1", wantErr: "want 0 <= min <= max <= 22"},
		{name: "tiles bbox west of east", src: "-- layout: tiles\n-- domain: tiles z0-2 bbox 30,35,-10,70\n-- max-age: 5\nSELECT 1", wantErr: "want -180 <= w < e <= 180"},
		{name: "tiles bbox past the Mercator limit", src: "-- layout: tiles\n-- domain: tiles z0-2 bbox -10,35,30,89\n-- max-age: 5\nSELECT 1", wantErr: "-85.0511 <= s < n <= 85.0511"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, sql, err := query.ParseHeader(tt.src)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("header = %+v, want %+v", got, tt.want)
			}
			if sql != tt.wantSQL {
				t.Errorf("sql = %q, want %q", sql, tt.wantSQL)
			}
		})
	}
}
