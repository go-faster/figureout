package json

import (
	"encoding/json"
	"reflect"
	"strconv"
	"time"

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
	// A type that parses itself from text takes the string spelling, exactly as
	// encoding/json hands it one: a JSON number is still a number, and the
	// kinded parser below reads it into the underlying type.
	if s, ok := n.Value.(string); ok && t.Text {
		return scalar.ParseText(t, s, "")
	}

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
		// These are strings on the wire in every JSON dialect, except a
		// duration declaring a unit, whose canonical spelling is a number.
		if num, ok := n.Value.(json.Number); ok && t.Kind == figureout.TypeDuration && t.Unit > 0 {
			return d.scaled(t, num)
		}
		s, ok := n.Value.(string)
		if !ok {
			if t.Kind == figureout.TypeDuration && t.Unit > 0 {
				return nil, errors.Errorf("want a number of %s or a duration string, got %s",
					t.UnitName(), n.Tag)
			}
			return nil, errors.Errorf("want a string, got %s", n.Tag)
		}
		return scalar.ParseText(t, s, "")

	default:
		return nil, errors.Errorf("cannot decode a %s from JSON", t.Kind)
	}
}

// DecodeAny implements [tree.ScalarDecoder].
//
// A JSON object is always string-keyed, so a passthrough is map[string]any all
// the way down. A number keeps its integer spelling where it has one:
// encoding/json would hand back a float64, and a passthrough carrying an
// identifier or a byte count must not lose digits on the way through.
func (d decoder) DecodeAny(n *tree.Node) (any, error) {
	switch n.Kind {
	case tree.Null:
		return nil, nil
	case tree.Array:
		out := make([]any, 0, len(n.Items))
		for i, item := range n.Items {
			v, err := d.DecodeAny(item)
			if err != nil {
				return nil, errors.Wrapf(err, "element %d", i)
			}
			out = append(out, v)
		}
		return out, nil
	case tree.Object:
		out := make(map[string]any, len(n.Fields))
		for _, f := range n.Fields {
			v, err := d.DecodeAny(f.Value)
			if err != nil {
				return nil, errors.Wrapf(err, "key %q", f.Key)
			}
			out[f.Key] = v
		}
		return out, nil
	default:
	}

	num, ok := n.Value.(json.Number)
	if !ok {
		return n.Value, nil
	}
	if i, err := num.Int64(); err == nil {
		return i, nil
	}
	f, err := num.Float64()
	if err != nil {
		return nil, errors.Errorf("invalid number %q", num)
	}
	return f, nil
}

// scaled reads a JSON number as a count of the field's declared unit.
func (decoder) scaled(t figureout.Type, num json.Number) (any, error) {
	var (
		d   time.Duration
		err error
	)
	if n, intErr := num.Int64(); intErr == nil {
		d, err = scalar.ScaleUnit(t, n)
	} else {
		f, floatErr := num.Float64()
		if floatErr != nil {
			return nil, errors.Errorf("invalid number %q", num)
		}
		d, err = scalar.ScaleUnitFloat(t, f)
	}
	if err != nil {
		return nil, err
	}
	return convert(t.Go, reflect.ValueOf(d))
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
