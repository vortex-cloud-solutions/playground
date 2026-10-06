// Package encode turns a query result into the column-major table the API
// and the frozen bucket serve.
package encode

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vortex-cloud-solutions/playground/internal/query"
)

// Table is the wire table: one array per column in Values. A text column in
// Dict holds indices into Dict[column]; a column named in Delta holds its
// first value, then differences.
type Table struct {
	Columns []string            `json:"columns"`
	Rows    int                 `json:"rows"`
	Values  [][]any             `json:"values"`
	Dict    map[string][]string `json:"dict"`
	Delta   []string            `json:"delta"`
}

// maxSafe is 2^53 - 1, the largest integer a JavaScript number holds exactly.
const maxSafe = 1<<53 - 1

type kind int

const (
	kindInt kind = iota
	kindFloat
	kindText
	kindBool
	kindDate
	kindTimestamptz
	kindJSON
)

var kinds = map[uint32]kind{
	pgtype.Int2OID: kindInt, pgtype.Int4OID: kindInt, pgtype.Int8OID: kindInt,
	pgtype.Float4OID: kindFloat, pgtype.Float8OID: kindFloat,
	pgtype.TextOID: kindText, pgtype.VarcharOID: kindText, pgtype.BPCharOID: kindText, pgtype.NameOID: kindText,
	pgtype.BoolOID:        kindBool,
	pgtype.DateOID:        kindDate,
	pgtype.TimestamptzOID: kindTimestamptz,
	pgtype.JSONOID:        kindJSON, pgtype.JSONBOID: kindJSON,
}

// Encode reads rows to the end and closes them. Text columns whose distinct
// count is at most half the row count are dictionary-coded; with layout
// delta, integer columns without NULLs whose differences stay within 2^53
// are delta-coded. Any other column type is an error: cast it in SQL.
func Encode(rows pgx.Rows, layout query.Layout) (Table, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	t := Table{
		Columns: make([]string, len(fields)),
		Values:  make([][]any, len(fields)),
		Dict:    map[string][]string{},
		Delta:   []string{},
	}
	colKinds := make([]kind, len(fields))
	seen := map[string]bool{}
	for i, f := range fields {
		k, ok := kinds[f.DataTypeOID]
		if !ok {
			return Table{}, fmt.Errorf("column %q: unsupported type OID %d; cast it in SQL (numeric to bigint or float8, geometry to json)", f.Name, f.DataTypeOID)
		}
		if seen[f.Name] {
			return Table{}, fmt.Errorf("column %q appears twice; alias it in SQL", f.Name)
		}
		seen[f.Name] = true
		t.Columns[i], colKinds[i], t.Values[i] = f.Name, k, []any{}
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return Table{}, err
		}
		for i, v := range vals {
			cv, err := convert(colKinds[i], v)
			if err != nil {
				return Table{}, fmt.Errorf("column %q, row %d: %w", t.Columns[i], t.Rows, err)
			}
			t.Values[i] = append(t.Values[i], cv)
		}
		t.Rows++
	}
	if err := rows.Err(); err != nil {
		return Table{}, err
	}
	for i, name := range t.Columns {
		switch {
		case colKinds[i] == kindText:
			if dict, idx, ok := dictCode(t.Values[i]); ok {
				t.Dict[name], t.Values[i] = dict, idx
			}
		case colKinds[i] == kindInt && layout == query.LayoutDelta:
			if d, ok := deltaCode(t.Values[i]); ok {
				t.Values[i] = d
				t.Delta = append(t.Delta, name)
			}
		}
	}
	return t, nil
}

func convert(k kind, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch k {
	case kindInt:
		var n int64
		switch x := v.(type) {
		case int16:
			n = int64(x)
		case int32:
			n = int64(x)
		case int64:
			n = x
		default:
			return nil, fmt.Errorf("unexpected %T for an integer column", v)
		}
		if n > maxSafe || n < -maxSafe {
			return nil, fmt.Errorf("integer %d is beyond ±(2^53-1), which JSON readers cannot hold exactly; cast it to text in SQL", n)
		}
		return n, nil
	case kindFloat:
		switch x := v.(type) {
		case float32:
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, errors.New("NaN or infinity has no JSON form")
			}
			return json.Number(strconv.FormatFloat(float64(x), 'g', -1, 32)), nil
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, errors.New("NaN or infinity has no JSON form")
			}
			return x, nil
		}
	case kindText:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case kindBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case kindDate:
		switch x := v.(type) {
		case time.Time:
			return x.Format(time.DateOnly), nil
		case string: // pgx decodes infinity and -infinity as strings
			return x, nil
		}
	case kindTimestamptz:
		switch x := v.(type) {
		case time.Time:
			return x.UTC().Format(time.RFC3339Nano), nil
		case string:
			return x, nil
		}
	case kindJSON:
		return v, nil
	}
	return nil, fmt.Errorf("unexpected %T", v)
}

// dictCode codes a text column as indices into its distinct values, in order
// of first appearance, when there are at most half as many distinct values
// as rows. NULL stays null.
func dictCode(values []any) ([]string, []any, bool) {
	if len(values) == 0 {
		return nil, nil, false
	}
	index := map[string]int{}
	dict := []string{}
	for _, v := range values {
		if s, ok := v.(string); ok {
			if _, seen := index[s]; !seen {
				index[s] = len(dict)
				dict = append(dict, s)
			}
		}
	}
	if 2*len(dict) > len(values) {
		return nil, nil, false
	}
	out := make([]any, len(values))
	for i, v := range values {
		if s, ok := v.(string); ok {
			out[i] = index[s]
		}
	}
	return dict, out, true
}

// deltaCode keeps the first value and replaces each later one with its
// difference from the previous. A NULL, or a difference beyond ±(2^53-1),
// leaves the column absolute.
func deltaCode(values []any) ([]any, bool) {
	out := make([]any, len(values))
	var prev int64
	for i, v := range values {
		n, ok := v.(int64)
		if !ok {
			return nil, false
		}
		if i == 0 {
			out[i] = n
		} else {
			d := n - prev
			if d > maxSafe || d < -maxSafe {
				return nil, false
			}
			out[i] = d
		}
		prev = n
	}
	return out, true
}
