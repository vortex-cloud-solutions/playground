package server

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vortex-cloud-solutions/playground/internal/query"
)

// maxText bounds a text parameter, in bytes.
const maxText = 256

// checkParam accepts a path segment only in the canonical form the domain
// endpoint produces, so every URL maps to exactly one frozen object.
func checkParam(t query.ParamType, s string) error {
	switch t {
	case query.ParamInt:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != s {
			return fmt.Errorf("%q is not a canonical integer", s)
		}
	case query.ParamFloat:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || strconv.FormatFloat(f, 'g', -1, 64) != s {
			return fmt.Errorf("%q is not a canonical finite float", s)
		}
	case query.ParamDate:
		d, err := time.Parse(time.DateOnly, s)
		if err != nil || d.Format(time.DateOnly) != s {
			return fmt.Errorf("%q is not a YYYY-MM-DD date", s)
		}
	case query.ParamText:
		switch {
		case s == "" || len(s) > maxText:
			return fmt.Errorf("text must be 1 to %d bytes", maxText)
		case !utf8.ValidString(s) || strings.ContainsRune(s, 0):
			return errors.New("text must be valid UTF-8 without NUL")
		case s == "." || s == "..":
			return fmt.Errorf("%q is not a usable path segment", s)
		}
	default:
		return fmt.Errorf("unknown param type %q", t)
	}
	return nil
}

// formatDomainValue renders one domain column the way checkParam accepts it.
func formatDomainValue(t query.ParamType, v any) (string, error) {
	var s string
	switch x := v.(type) {
	case int16:
		s = strconv.FormatInt(int64(x), 10)
	case int32:
		s = strconv.FormatInt(int64(x), 10)
	case int64:
		s = strconv.FormatInt(x, 10)
	case float32:
		s = strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		s = strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		s = x.Format(time.DateOnly)
	case string:
		s = x
	case nil:
		return "", errors.New("NULL in a domain")
	default:
		return "", fmt.Errorf("unsupported domain value %T", v)
	}
	if err := checkParam(t, s); err != nil {
		return "", fmt.Errorf("domain value for a %s param: %w", t, err)
	}
	return s, nil
}
