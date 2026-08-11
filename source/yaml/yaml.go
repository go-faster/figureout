// Package yaml reads configuration from YAML documents.
//
// It is a separate adapter from JSON: YAML resolves scalars by tag rather than
// by syntax, has several spellings of null, and carries its own positions.
package yaml

import (
	"context"
	"io"
	"os"

	"github.com/go-faster/errors"
	"github.com/go-faster/yaml"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/tree"
)

// Source identifies the YAML source.
const Source = figureout.SourceID("yaml")

// Option customizes the YAML source.
type Option interface {
	applyYAML(*source) error
}

type optionFunc func(*source) error

func (f optionFunc) applyYAML(s *source) error { return f(s) }

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

// Separator sets the list separator used when a list is written as a single
// scalar. The default is a comma.
func Separator(sep string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if sep == "" {
			return errors.New("empty separator")
		}
		return c.AddSourceOption(Source, separatorOption{sep})
	})
}

type separatorOption struct{ sep string }

type source struct {
	file            string
	data            []byte
	read            func() ([]byte, error)
	disallowUnknown bool
	optional        bool
	err             error
}

// File reads a YAML document from disk.
func File(path string, opts ...Option) figureout.Source {
	return newSource(&source{
		file: path,
		read: func() ([]byte, error) {
			// The path is the configuration file the caller named.
			return os.ReadFile(path) //nolint:gosec // G304: caller-supplied path is the point
		},
	}, opts)
}

// Bytes reads a YAML document from memory.
func Bytes(data []byte, opts ...Option) figureout.Source {
	return newSource(&source{data: data}, opts)
}

// Reader reads a YAML document from r.
func Reader(r io.Reader, opts ...Option) figureout.Source {
	return newSource(&source{
		read: func() ([]byte, error) { return io.ReadAll(r) },
	}, opts)
}

func newSource(s *source, opts []Option) figureout.Source {
	for _, o := range opts {
		if err := o.applyYAML(s); err != nil {
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

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, errors.Wrapf(err, "parse %s", s.name())
	}

	root, err := convertNode(&doc)
	if err != nil {
		return nil, errors.Wrapf(err, "parse %s", s.name())
	}
	if root == nil {
		return &figureout.Layer{Source: Source}, nil
	}

	return tree.Binder{
		Source:          Source,
		File:            s.file,
		Decoder:         decoder{},
		DisallowUnknown: s.disallowUnknown,
		// YAML spells null as ~ or null, so a document can erase what an
		// earlier layer set.
		AllowNull: true,
	}.Bind(m, root), nil
}

func (s *source) name() string {
	if s.file != "" {
		return s.file
	}
	return "yaml document"
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
//
// It names a configuration alias, not a YAML anchor alias.
func Alias(names ...string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if len(names) == 0 {
			return errors.New("no aliases given")
		}
		return c.AddSourceOption(Source, tree.AliasOption{Names: names})
	})
}

// Skip excludes a field from the YAML source.
func Skip() figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		return c.SkipSource(Source)
	})
}

// ProjectNames implements [figureout.SourceNamer].
func (s *source) ProjectNames(m *figureout.Model) map[string][]string {
	return tree.Names(m, Source)
}
