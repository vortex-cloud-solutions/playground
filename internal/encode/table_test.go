package encode_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vortex-cloud-solutions/playground/internal/encode"
	"github.com/vortex-cloud-solutions/playground/internal/pgfake"
	"github.com/vortex-cloud-solutions/playground/internal/query"
)

var col = pgfake.Col

func TestEncode(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	cest := time.FixedZone("CEST", 2*60*60)
	tests := []struct {
		name   string
		layout query.Layout
		rows   *pgfake.Rows
		want   string
	}{
		{
			name:   "text column with few distinct values is dictionary-coded",
			layout: query.LayoutColumns,
			rows: pgfake.NewRows([]pgfake.Column{col("country", pgtype.TextOID), col("n", pgtype.Int4OID)},
				[]any{"AT", int32(1)}, []any{"AT", int32(2)}, []any{"BE", int32(3)}, []any{"AT", int32(4)}),
			want: `{"columns":["country","n"],"rows":4,"values":[[0,0,1,0],[1,2,3,4]],"dict":{"country":["AT","BE"]},"delta":[]}`,
		},
		{
			name:   "text column with more distinct values than half the rows stays plain",
			layout: query.LayoutColumns,
			rows: pgfake.NewRows([]pgfake.Column{col("word", pgtype.VarcharOID)},
				[]any{"a"}, []any{"b"}, []any{"a"}),
			want: `{"columns":["word"],"rows":3,"values":[["a","b","a"]],"dict":{},"delta":[]}`,
		},
		{
			name:   "NULL in a dictionary-coded column stays null",
			layout: query.LayoutColumns,
			rows: pgfake.NewRows([]pgfake.Column{col("position", pgtype.TextOID)},
				[]any{"for"}, []any{nil}, []any{"for"}, []any{"against"}),
			want: `{"columns":["position"],"rows":4,"values":[[0,null,0,1]],"dict":{"position":["for","against"]},"delta":[]}`,
		},
		{
			name:   "delta layout codes integer columns and leaves one with a NULL absolute",
			layout: query.LayoutDelta,
			rows: pgfake.NewRows([]pgfake.Column{col("ts", pgtype.Int8OID), col("mw", pgtype.Int4OID), col("zone", pgtype.TextOID)},
				[]any{int64(1000), int32(5), "NL"}, []any{int64(1060), int32(7), "NL"},
				[]any{int64(1120), int32(-2), "NL"}, []any{int64(1120), nil, "NL"}),
			want: `{"columns":["ts","mw","zone"],"rows":4,"values":[[1000,60,60,0],[5,7,-2,null],[0,0,0,0]],"dict":{"zone":["NL"]},"delta":["ts"]}`,
		},
		{
			name:   "columns layout never delta-codes",
			layout: query.LayoutColumns,
			rows:   pgfake.NewRows([]pgfake.Column{col("ts", pgtype.Int8OID)}, []any{int64(1000)}, []any{int64(1060)}),
			want:   `{"columns":["ts"],"rows":2,"values":[[1000,1060]],"dict":{},"delta":[]}`,
		},
		{
			name:   "a difference beyond 2^53 leaves the column absolute",
			layout: query.LayoutDelta,
			rows: pgfake.NewRows([]pgfake.Column{col("id", pgtype.Int8OID)},
				[]any{int64(-9007199254740991)}, []any{int64(9007199254740991)}),
			want: `{"columns":["id"],"rows":2,"values":[[-9007199254740991,9007199254740991]],"dict":{},"delta":[]}`,
		},
		{
			name:   "int2 is an integer column",
			layout: query.LayoutDelta,
			rows:   pgfake.NewRows([]pgfake.Column{col("h", pgtype.Int2OID)}, []any{int16(3)}, []any{int16(1)}),
			want:   `{"columns":["h"],"rows":2,"values":[[3,-2]],"dict":{},"delta":["h"]}`,
		},
		{
			name:   "every other supported type",
			layout: query.LayoutColumns,
			rows: pgfake.NewRows([]pgfake.Column{
				col("ok", pgtype.BoolOID), col("f8", pgtype.Float8OID), col("f4", pgtype.Float4OID),
				col("day", pgtype.DateOID), col("at", pgtype.TimestamptzOID), col("doc", pgtype.JSONBOID),
			}, []any{true, 2.5, float32(0.1), day(6), time.Date(2026, 10, 6, 12, 0, 0, 0, cest), map[string]any{"a": float64(1)}}),
			want: `{"columns":["ok","f8","f4","day","at","doc"],"rows":1,"values":[[true],[2.5],[0.1],["2026-10-06"],["2026-10-06T10:00:00Z"],[{"a":1}]],"dict":{},"delta":[]}`,
		},
		{
			name:   "no rows: empty columns, nothing dictionary-coded",
			layout: query.LayoutColumns,
			rows:   pgfake.NewRows([]pgfake.Column{col("country", pgtype.TextOID), col("n", pgtype.Int4OID)}),
			want:   `{"columns":["country","n"],"rows":0,"values":[[],[]],"dict":{},"delta":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table, err := encode.Encode(tt.rows, tt.layout)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(table)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestEncodeRejects(t *testing.T) {
	tests := []struct {
		name    string
		rows    *pgfake.Rows
		wantErr string
	}{
		{
			name:    "numeric must be cast in SQL",
			rows:    pgfake.NewRows([]pgfake.Column{col("eur", pgtype.NumericOID)}),
			wantErr: `column "eur": unsupported type OID 1700`,
		},
		{
			name:    "an integer JavaScript cannot hold",
			rows:    pgfake.NewRows([]pgfake.Column{col("h3", pgtype.Int8OID)}, []any{int64(9007199254740991)}, []any{int64(9007199254740992)}),
			wantErr: `column "h3", row 1: integer 9007199254740992 is beyond ±(2^53-1)`,
		},
		{
			name:    "NaN",
			rows:    pgfake.NewRows([]pgfake.Column{col("x", pgtype.Float8OID)}, []any{math.NaN()}),
			wantErr: `column "x", row 0: NaN or infinity has no JSON form`,
		},
		{
			name:    "duplicate column names",
			rows:    pgfake.NewRows([]pgfake.Column{col("n", pgtype.Int4OID), col("n", pgtype.Int4OID)}),
			wantErr: `column "n" appears twice`,
		},
		{
			name:    "the query failed on the server",
			rows:    pgfake.ErrRows(errors.New("canceling statement due to statement timeout")),
			wantErr: "canceling statement due to statement timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encode.Encode(tt.rows, query.LayoutDelta)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
