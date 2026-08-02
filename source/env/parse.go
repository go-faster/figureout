package env

import (
	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/scalar"
)

const defaultSeparator = scalar.DefaultSeparator

// parse converts a raw variable value into a semantic value of the field's Go
// type. Environment variables are text, so the shared text scalar parser
// decides what every representation means.
func parse(t figureout.Type, raw, sep string) (any, error) {
	return scalar.ParseText(t, raw, sep)
}
