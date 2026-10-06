// Package query parses the named-query files that modules ship as
// modules/<module>/queries/<name>.sql and holds them in a Registry.
package query

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ParamType is the declared type of a path parameter.
type ParamType string

const (
	ParamInt   ParamType = "int"
	ParamText  ParamType = "text"
	ParamDate  ParamType = "date"
	ParamFloat ParamType = "float"
)

// Param is one positional parameter, bound as $1..$n in declaration order.
type Param struct {
	Name string    `json:"name"`
	Type ParamType `json:"type"`
}

// Layout selects how the API encodes a result.
type Layout string

const (
	LayoutColumns Layout = "columns"
	LayoutDelta   Layout = "delta"
	LayoutTiles   Layout = "tiles"
)

// Header is the parsed `-- key: value` block at the top of a query file.
type Header struct {
	Params         []Param
	Domain         string
	Layout         Layout
	MaxAge         int
	LiveOnly       bool
	FrozenFallback string
}

// maxMaxAge is one year: the max-age the frozen bucket gives hashed objects.
const maxMaxAge = 31536000

var (
	headerLine = regexp.MustCompile(`^-- ([a-z][a-z-]*):(.*)$`)
	paramName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	queryName  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	tileDomain = regexp.MustCompile(`^tiles z(\d{1,2})-(\d{1,2}) bbox (-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)$`)
)

var knownKeys = []string{"params", "domain", "layout", "max-age", "live-only", "frozen-fallback"}

// tileParams are the params a tiles query has, declared or not.
var tileParams = []Param{{Name: "z", Type: ParamInt}, {Name: "x", Type: ParamInt}, {Name: "y", Type: ParamInt}}

// ParseHeader splits a query file into its header and its SQL. The header is
// the run of leading lines that start with "--"; each must read
// `-- key: value` with a known key, at most once. The first line that does
// not start with "--" begins the SQL, which is returned trimmed.
func ParseHeader(src string) (Header, string, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	raw := map[string]string{}
	i := 0
	for ; i < len(lines) && strings.HasPrefix(lines[i], "--"); i++ {
		m := headerLine.FindStringSubmatch(lines[i])
		if m == nil {
			return Header{}, "", fmt.Errorf("line %d: header lines read `-- key: value`, got %q", i+1, lines[i])
		}
		key, value := m[1], strings.TrimSpace(m[2])
		if !slices.Contains(knownKeys, key) {
			return Header{}, "", fmt.Errorf("line %d: unknown header key %q", i+1, key)
		}
		if _, dup := raw[key]; dup {
			return Header{}, "", fmt.Errorf("line %d: duplicate header key %q", i+1, key)
		}
		raw[key] = value
	}
	sql := strings.TrimSpace(strings.Join(lines[i:], "\n"))
	if sql == "" {
		return Header{}, "", errors.New("no SQL after the header")
	}
	h, err := buildHeader(raw)
	if err != nil {
		return Header{}, "", err
	}
	return h, sql, nil
}

func buildHeader(raw map[string]string) (Header, error) {
	var h Header

	layout, ok := raw["layout"]
	if !ok {
		return Header{}, errors.New(`missing header key "layout"`)
	}
	switch l := Layout(layout); l {
	case LayoutColumns, LayoutDelta, LayoutTiles:
		h.Layout = l
	default:
		return Header{}, fmt.Errorf("layout %q: want columns, delta or tiles", layout)
	}

	params, hasParams := raw["params"]
	if h.Layout == LayoutTiles {
		if hasParams {
			got, err := parseParams(params)
			if err != nil || !slices.Equal(got, tileParams) {
				return Header{}, errors.New("layout tiles implies params z:int,x:int,y:int: omit params or declare exactly those")
			}
		}
		h.Params = slices.Clone(tileParams)
	} else {
		if !hasParams {
			return Header{}, errors.New(`missing header key "params" (leave it empty for none)`)
		}
		got, err := parseParams(params)
		if err != nil {
			return Header{}, err
		}
		h.Params = got
	}

	maxAge, ok := raw["max-age"]
	if !ok {
		return Header{}, errors.New(`missing header key "max-age"`)
	}
	n, err := strconv.Atoi(maxAge)
	if err != nil || n < 0 || n > maxMaxAge {
		return Header{}, fmt.Errorf("max-age %q: want whole seconds from 0 to %d", maxAge, maxMaxAge)
	}
	h.MaxAge = n

	switch v := raw["live-only"]; v {
	case "", "false":
	case "true":
		h.LiveOnly = true
	default:
		return Header{}, fmt.Errorf("live-only %q: want true or false", v)
	}

	h.Domain = raw["domain"]
	if !h.LiveOnly {
		switch {
		case h.Layout == LayoutTiles:
			if err := checkTileDomain(h.Domain); err != nil {
				return Header{}, err
			}
		case len(h.Params) == 0:
			if h.Domain != "" {
				return Header{}, errors.New("domain must be empty on a query without params: it has exactly one answer")
			}
		case h.Domain == "":
			return Header{}, errors.New("domain is required unless live-only: true")
		}
	}

	h.FrozenFallback = raw["frozen-fallback"]
	if h.FrozenFallback != "" {
		if !h.LiveOnly {
			return Header{}, errors.New("frozen-fallback is only allowed on a live-only query")
		}
		if !queryName.MatchString(h.FrozenFallback) {
			return Header{}, fmt.Errorf("frozen-fallback %q is not a query name", h.FrozenFallback)
		}
	}
	return h, nil
}

func parseParams(s string) ([]Param, error) {
	params := []Param{}
	if s == "" {
		return params, nil
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		name, typ, ok := strings.Cut(item, ":")
		if !ok {
			return nil, fmt.Errorf("param %q: want name:type", item)
		}
		if !paramName.MatchString(name) {
			return nil, fmt.Errorf("param %q: name must match %s", name, paramName)
		}
		switch ParamType(typ) {
		case ParamInt, ParamText, ParamDate, ParamFloat:
		default:
			return nil, fmt.Errorf("param %q: type %q: want int, text, date or float", name, typ)
		}
		if seen[name] {
			return nil, fmt.Errorf("param %q declared twice", name)
		}
		seen[name] = true
		params = append(params, Param{Name: name, Type: ParamType(typ)})
	}
	return params, nil
}

// checkTileDomain validates `tiles z<min>-<max> bbox <w>,<s>,<e>,<n>`.
// Nothing enumerates the tiles until the .mvt route exists; this only
// refuses a malformed domain.
func checkTileDomain(s string) error {
	m := tileDomain.FindStringSubmatch(s)
	if m == nil {
		return fmt.Errorf("domain %q: a tiles query wants `tiles z<min>-<max> bbox <w>,<s>,<e>,<n>`", s)
	}
	zmin, _ := strconv.Atoi(m[1])
	zmax, _ := strconv.Atoi(m[2])
	if zmin > zmax || zmax > 22 {
		return fmt.Errorf("domain %q: want 0 <= min <= max <= 22", s)
	}
	var b [4]float64
	for i := range b {
		b[i], _ = strconv.ParseFloat(m[3+i], 64)
	}
	w, south, e, north := b[0], b[1], b[2], b[3]
	if w < -180 || e > 180 || w >= e || south < -85.0511 || north > 85.0511 || south >= north {
		return fmt.Errorf("domain %q: want -180 <= w < e <= 180 and -85.0511 <= s < n <= 85.0511", s)
	}
	return nil
}
