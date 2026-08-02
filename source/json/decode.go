package json

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/scalar"
	"github.com/go-faster/figureout/internal/tree"
)

// decoder applies JSON's own scalar rules.
//
// Unlike environment variables, JSON distinguishes 8080 from "8080", so a
// numeric field takes a JSON number. A field may still accept a string by
// declaring it with [Accepts]; then, and only then, the text is parsed.
type decoder struct{}

// DecodeScalar implements [tree.ScalarDecoder].
func (d decoder) DecodeScalar(t figureout.Type, n *tree.Node, accepts []figureout.Shape) (any, error) {
	switch t.Kind {
	case figureout.TypeBoolean:
		b, ok := n.Value.(bool)
		if !ok {
			return d.fromString(t, n, accepts)
		}
		return convert(t.Go, reflect.ValueOf(b))

	case figureout.TypeInteger:
		num, ok := n.Value.(json.Number)
		if !ok {
			return d.fromString(t, n, accepts)
		}
		return parseInteger(t.Go, num.String())

	case figureout.TypeNumber:
		num, ok := n.Value.(json.Number)
		if !ok {
			return d.fromString(t, n, accepts)
		}
		f, err := num.Float64()
		if err != nil {
			return nil, errors.Errorf("invalid number %q", num)
		}
		return convert(t.Go, reflect.ValueOf(f))

	case figureout.TypeString:
		s, ok := n.Value.(string)
		if !ok {
			return nil, errors.Errorf("want a string, got %s", n.Tag)
		}
		return convert(t.Go, reflect.ValueOf(s))

	case figureout.TypeDuration, figureout.TypeTimestamp, figureout.TypeBytes:
		// These are strings on the wire in every JSON dialect.
		s, ok := n.Value.(string)
		if !ok {
			return nil, errors.Errorf("want a string, got %s", n.Tag)
		}
		return scalar.ParseText(t, s, "")

	default:
		return nil, errors.Errorf("cannot decode a %s from JSON", t.Kind)
	}
}

// fromString accepts a JSON string for a non-string field, but only when the
// field declared that it takes one.
func (d decoder) fromString(t figureout.Type, n *tree.Node, accepts []figureout.Shape) (any, error) {
	s, ok := n.Value.(string)
	if !ok {
		return nil, errors.Errorf("want %s, got %s", jsonName(t.Kind), n.Tag)
	}
	if !acceptsString(accepts) {
		return nil, errors.Errorf(
			"want %s, got string; declare json.Accepts(json.String()) to accept one",
			jsonName(t.Kind),
		)
	}
	return scalar.ParseText(t, s, "")
}

func acceptsString(accepts []figureout.Shape) bool {
	for _, s := range accepts {
		if s.Kind == figureout.ShapeString {
			return true
		}
	}
	return false
}

func jsonName(k figureout.TypeKind) string {
	switch k {
	case figureout.TypeBoolean:
		return "a boolean"
	case figureout.TypeInteger, figureout.TypeNumber:
		return "a number"
	default:
		return "a " + k.String()
	}
}

func parseInteger(want reflect.Type, raw string) (any, error) {
	switch want.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, want.Bits())
		if err != nil {
			return nil, errors.Errorf("invalid unsigned integer %q", raw)
		}
		return reflect.ValueOf(v).Convert(want).Interface(), nil
	default:
		v, err := strconv.ParseInt(raw, 10, want.Bits())
		if err != nil {
			return nil, errors.Errorf("invalid integer %q", raw)
		}
		return reflect.ValueOf(v).Convert(want).Interface(), nil
	}
}

func convert(want reflect.Type, v reflect.Value) (any, error) {
	if !v.Type().ConvertibleTo(want) {
		return nil, errors.Errorf("cannot use %s as %s", v.Type(), want)
	}
	return v.Convert(want).Interface(), nil
}
