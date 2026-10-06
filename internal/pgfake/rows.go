// Package pgfake is an in-memory pgx.Rows for unit tests of code that
// encodes query results. Values are given already decoded, as pgx's
// Rows.Values returns them (int32 for int4, time.Time for date, ...).
package pgfake

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Column is one result column: its name and Postgres type OID (pgtype.*OID).
type Column struct {
	Name string
	OID  uint32
}

// Col is shorthand for Column{Name: name, OID: oid}.
func Col(name string, oid uint32) Column { return Column{Name: name, OID: oid} }

// Rows implements pgx.Rows over fixed data.
type Rows struct {
	fields []pgconn.FieldDescription
	data   [][]any
	err    error
	next   int
	closed bool
}

var _ pgx.Rows = (*Rows)(nil)

// NewRows returns rows with the given columns and data, one []any per row.
func NewRows(cols []Column, data ...[]any) *Rows {
	fields := make([]pgconn.FieldDescription, len(cols))
	for i, c := range cols {
		fields[i] = pgconn.FieldDescription{Name: c.Name, DataTypeOID: c.OID, Format: pgtype.TextFormatCode}
	}
	return &Rows{fields: fields, data: data}
}

// ErrRows returns rows that yield nothing and report err from Err, the way
// pgx reports a query that failed on the server.
func ErrRows(err error) *Rows { return &Rows{err: err} }

func (r *Rows) Close()     { r.closed = true }
func (r *Rows) Err() error { return r.err }

func (r *Rows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag(fmt.Sprintf("SELECT %d", len(r.data)))
}

func (r *Rows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }

func (r *Rows) Next() bool {
	if r.closed || r.next >= len(r.data) {
		r.closed = true
		return false
	}
	r.next++
	return true
}

func (r *Rows) Scan(dest ...any) error {
	return errors.New("pgfake: Scan is not implemented, use Values")
}

func (r *Rows) Values() ([]any, error) {
	if r.next == 0 || r.next > len(r.data) {
		return nil, errors.New("pgfake: Values called without a current row")
	}
	return r.data[r.next-1], nil
}

func (r *Rows) RawValues() [][]byte  { return nil }
func (r *Rows) Conn() *pgx.Conn      { return nil }
func (r *Rows) TypeMap() *pgtype.Map { return pgtype.NewMap() }
