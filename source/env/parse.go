package env

import (
	"encoding/base64"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout"
)

const defaultSeparator = ","

// parse converts a raw variable value into a semantic value of the field's Go
// type.
func parse(t figureout.Type, raw string, sep string) (any, error) {
	if sep == "" {
		sep = defaultSeparator
	}

	switch t.Kind {
	case figureout.TypeBoolean:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, errors.Errorf("invalid boolean %q", raw)
		}
		return convert(t.Go, reflect.ValueOf(v))

	case figureout.TypeInteger:
		return parseInteger(t.Go, raw)

	case figureout.TypeNumber:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, errors.Errorf("invalid number %q", raw)
		}
		return convert(t.Go, reflect.ValueOf(v))

	case figureout.TypeString:
		return convert(t.Go, reflect.ValueOf(raw))

	case figureout.TypeDuration:
		v, err := time.ParseDuration(raw)
		if err != nil {
			return nil, errors.Errorf("invalid duration %q", raw)
		}
		return convert(t.Go, reflect.ValueOf(v))

	case figureout.TypeTimestamp:
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, errors.Errorf("invalid timestamp %q, want RFC 3339", raw)
		}
		return convert(t.Go, reflect.ValueOf(v))

	case figureout.TypeBytes:
		v, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, errors.Errorf("invalid base64 value")
		}
		return convert(t.Go, reflect.ValueOf(v))

	case figureout.TypeList:
		return parseList(t, raw, sep)

	default:
		return nil, errors.Errorf("environment variables cannot represent a %s field", t.Kind)
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

func parseList(t figureout.Type, raw string, sep string) (any, error) {
	if t.Elem == nil {
		return nil, errors.New("list without element type")
	}
	if t.Go.Kind() != reflect.Slice {
		return nil, errors.Errorf("cannot decode a list into %s", t.Go)
	}

	parts := strings.Split(raw, sep)
	if raw == "" {
		parts = nil
	}
	out := reflect.MakeSlice(t.Go, 0, len(parts))
	for i, p := range parts {
		v, err := parse(*t.Elem, strings.TrimSpace(p), sep)
		if err != nil {
			return nil, errors.Wrapf(err, "element %d", i)
		}
		out = reflect.Append(out, reflect.ValueOf(v))
	}
	return out.Interface(), nil
}

func convert(want reflect.Type, v reflect.Value) (any, error) {
	if !v.Type().ConvertibleTo(want) {
		return nil, errors.Errorf("cannot use %s as %s", v.Type(), want)
	}
	return v.Convert(want).Interface(), nil
}
