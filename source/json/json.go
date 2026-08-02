// Package json reads configuration from JSON documents.
//
// It is a separate adapter from YAML and TOML on purpose: the formats differ
// in scalar syntax, null handling and numeric precision, and the diagnostics
// name the format the user actually wrote.
package json

import (
	"context"
	"io"
	"os"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/tree"
)

// Source identifies the JSON source.
const Source = figureout.SourceID("json")

// Option customizes the JSON source.
type Option interface {
	applyJSON(*source) error
}

type optionFunc func(*source) error

func (f optionFunc) applyJSON(s *source) error { return f(s) }

// DisallowUnknownFields reports document members that no field claims.
func DisallowUnknownFields() Option {
	return optionFunc(func(s *source) error {
		s.disallowUnknown = true
		return nil
	})
}

// Optional makes a missing file produce an empty layer instead of an error.
func Optional() Option {
	return optionFunc(func(s *source) error {
		s.optional = true
		return nil
	})
}

type source struct {
	file            string
	data            []byte
	read            func() ([]byte, error)
	disallowUnknown bool
	optional        bool
	err             error
}

// File reads a JSON document from disk.
func File(path string, opts ...Option) figureout.Source {
	return newSource(&source{
		file: path,
		read: func() ([]byte, error) {
			// The path is the configuration file the caller named.
			return os.ReadFile(path) //nolint:gosec // G304: caller-supplied path is the point
		},
	}, opts)
}

// Bytes reads a JSON document from memory.
func Bytes(data []byte, opts ...Option) figureout.Source {
	return newSource(&source{data: data}, opts)
}

// Reader reads a JSON document from r.
func Reader(r io.Reader, opts ...Option) figureout.Source {
	return newSource(&source{
		read: func() ([]byte, error) { return io.ReadAll(r) },
	}, opts)
}

func newSource(s *source, opts []Option) figureout.Source {
	for _, o := range opts {
		if err := o.applyJSON(s); err != nil {
			s.err = err
			break
		}
	}
	return s
}

// ID implements [figureout.Source].
func (s *source) ID() figureout.SourceID { return Source }

// Load implements [figureout.Source].
func (s *source) Load(_ context.Context, m *figureout.Model) (*figureout.Layer, error) {
	if s.err != nil {
		return nil, s.err
	}

	data := s.data
	if s.read != nil {
		var err error
		if data, err = s.read(); err != nil {
			if s.optional && os.IsNotExist(err) {
				return &figureout.Layer{Source: Source}, nil
			}
			return nil, err
		}
	}

	root, err := parse(data)
	if err != nil {
		return nil, errors.Wrapf(err, "parse %s", s.name())
	}

	return tree.Binder{
		Source:          Source,
		File:            s.file,
		Decoder:         decoder{},
		DisallowUnknown: s.disallowUnknown,
		// JSON has a null literal, so a document can erase what an earlier
		// layer set.
		AllowNull: true,
	}.Bind(m, root), nil
}

func (s *source) name() string {
	if s.file != "" {
		return s.file
	}
	return "json document"
}

// Name overrides the property name bound to a field.
func Name(name string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if name == "" {
			return errors.New("empty property name")
		}
		return c.SetSourceNames(Source, name)
	})
}

// Alias adds accepted property names, tried after the primary name.
func Alias(names ...string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if len(names) == 0 {
			return errors.New("no aliases given")
		}
		return c.AddSourceOption(Source, tree.AliasOption{Names: names})
	})
}

// Skip excludes a field from the JSON source.
func Skip() figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		return c.SkipSource(Source)
	})
}

// Accepts declares the JSON types a field accepts, beyond the one its
// semantic type implies.
//
// The declaration drives decoding and schema generation together, so a field
// that accepts a string as well as an integer decodes both and says so in its
// JSON Schema.
func Accepts(shapes ...figureout.Shape) figureout.FieldOption {
	return figureout.AcceptShapes(Source, shapes...)
}

// Shape constructors for [Accepts].
func Integer() figureout.Shape { return figureout.Shape{Kind: figureout.ShapeInteger} }
func Number() figureout.Shape  { return figureout.Shape{Kind: figureout.ShapeNumber} }
func String() figureout.Shape  { return figureout.Shape{Kind: figureout.ShapeString} }
func Boolean() figureout.Shape { return figureout.Shape{Kind: figureout.ShapeBoolean} }
