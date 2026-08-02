// Package env projects a configuration descriptor onto environment variables.
package env

import (
	"context"
	"os"
	"strings"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout"
)

// Source identifies the environment variable source.
const Source = figureout.SourceID("env")

// Option customizes the environment source.
type Option interface {
	applyEnv(*source) error
}

type optionFunc func(*source) error

func (f optionFunc) applyEnv(s *source) error { return f(s) }

// Prefix prepends a fixed prefix to every derived variable name.
func Prefix(p string) Option {
	return optionFunc(func(s *source) error {
		s.prefix = p
		return nil
	})
}

type source struct {
	prefix string
	vars   map[string]string
}

// Current reads the process environment.
func Current(opts ...Option) figureout.Source {
	vars := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return newSource(vars, opts)
}

// Values reads an explicit set of variables. It is the hermetic form of
// [Current], intended for tests and for embedding.
func Values(vars map[string]string, opts ...Option) figureout.Source {
	return newSource(vars, opts)
}

func newSource(vars map[string]string, opts []Option) figureout.Source {
	s := &source{vars: vars}
	for _, o := range opts {
		if err := o.applyEnv(s); err != nil {
			return &failing{err: err}
		}
	}
	return s
}

type failing struct{ err error }

func (f *failing) ID() figureout.SourceID { return Source }

func (f *failing) Load(context.Context, *figureout.Model) (*figureout.Layer, error) {
	return nil, f.err
}

// ID implements [figureout.Source].
func (s *source) ID() figureout.SourceID { return Source }

// Load implements [figureout.Source].
func (s *source) Load(_ context.Context, m *figureout.Model) (*figureout.Layer, error) {
	layer := &figureout.Layer{Source: Source}
	plan, diags := s.plan(m)
	layer.Diagnostics = append(layer.Diagnostics, diags...)
	if layer.Diagnostics.HasErrors() {
		return layer, nil
	}

	for _, e := range plan {
		name, raw, ok := s.lookup(e.names)
		if !ok {
			continue
		}
		origin := figureout.Origin{Source: Source, Name: name}

		if e.discriminator {
			layer.Set(e.path, raw, origin)
			continue
		}

		if literal, ok := nullLiteralOf(e.field); ok && raw == literal {
			// An erase directive, not a value: it drops what earlier layers
			// set, so the field falls back to its default or to missing.
			layer.SetNull(e.path, origin)
			continue
		}

		v, err := parse(e.field.Type, raw, separatorOf(e.field))
		if err != nil {
			layer.Diagnostics = append(layer.Diagnostics, figureout.Diagnostic{
				Severity:  figureout.SeverityError,
				Code:      figureout.CodeSourceUnsupported,
				Message:   err.Error(),
				FieldPath: e.path,
				GoPath:    e.field.GoName,
				Source:    Source,
				Origin:    &origin,
			})
			continue
		}
		layer.Set(e.path, v, origin)
	}
	return layer, nil
}

func (s *source) lookup(names []string) (string, string, bool) {
	for _, n := range names {
		if v, ok := s.vars[n]; ok {
			return n, v, true
		}
	}
	return "", "", false
}

// entry is one environment variable binding.
type entry struct {
	path  string
	names []string
	field *figureout.FieldModel
	// discriminator marks a union tag rather than a semantic value.
	discriminator bool
}

// plan derives variable names for every field and checks for collisions.
func (s *source) plan(m *figureout.Model) ([]entry, figureout.Diagnostics) {
	var (
		out    []entry
		diags  figureout.Diagnostics
		byName = map[string]string{}
	)

	add := func(e entry) {
		for _, n := range e.names {
			if prev, ok := byName[n]; ok {
				diags = append(diags, figureout.Diagnostic{
					Severity: figureout.SeverityError,
					Code:     figureout.CodeSourceNameCollision,
					Source:   Source,
					Message:  "environment variable " + n + " is assigned to both " + prev + " and " + e.path,
				})
				return
			}
			byName[n] = e.path
		}
		out = append(out, e)
	}

	for _, f := range m.Fields() {
		if p, ok := f.Source(Source); ok && p.Skip {
			continue
		}
		if f.Type.Object != nil {
			continue // leaves are bound individually
		}
		if path, ok := figureout.DiscriminatorPath(f); ok {
			add(entry{path: path, names: s.names(f, path), field: f, discriminator: true})
			continue
		}
		add(entry{path: f.Path, names: s.names(f, f.Path), field: f})
	}
	return out, diags
}

// names returns the accepted variable names, primary first, aliases after.
func (s *source) names(f *figureout.FieldModel, path string) []string {
	primary := derive(path)
	var aliases []string
	if p, ok := f.Source(Source); ok {
		if len(p.Names) > 0 {
			primary = p.Names[0]
		}
		for _, o := range p.Options {
			if a, ok := o.(aliasOption); ok {
				aliases = append(aliases, a.names...)
			}
		}
	}

	out := make([]string, 0, 1+len(aliases))
	for _, n := range append([]string{primary}, aliases...) {
		out = append(out, s.prefix+n)
	}
	return out
}

// derive converts a canonical path to an environment variable name:
// "server.listenPort" becomes "SERVER_LISTEN_PORT".
func derive(path string) string {
	var sb strings.Builder
	for i, r := range path {
		switch {
		case r == '.' || r == '-':
			sb.WriteByte('_')
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				sb.WriteByte('_')
			}
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	return strings.ToUpper(sb.String())
}

// Name overrides the derived variable name. The descriptor prefix still
// applies.
func Name(name string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if name == "" {
			return errors.New("empty environment variable name")
		}
		return c.SetSourceNames(Source, name)
	})
}

// Alias adds accepted variable names, tried after the primary name.
//
// Unlike [Name], an alias never replaces the derived name.
func Alias(names ...string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if len(names) == 0 {
			return errors.New("no aliases given")
		}
		return c.AddSourceOption(Source, aliasOption{names})
	})
}

type aliasOption struct{ names []string }

// Skip excludes a field from the environment source.
func Skip() figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		return c.SkipSource(Source)
	})
}

// Separator sets the list separator for a field. The default is a comma.
func Separator(sep string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if sep == "" {
			return errors.New("empty separator")
		}
		return c.AddSourceOption(Source, separatorOption{sep})
	})
}

type separatorOption struct{ sep string }

// NullLiteral declares the value that erases a field.
//
// Environment variables have no null, only text, so the literal is opt-in per
// field: without it, "null" is just a string and stays one.
//
//	figureout.Optional(s, &c.Timeout, "timeout", env.NullLiteral("null"))
//
//	APP_TIMEOUT=null   erases whatever an earlier layer set
func NullLiteral(literal string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if literal == "" {
			return errors.New("empty null literal")
		}
		return c.AddSourceOption(Source, nullLiteralOption{literal})
	})
}

type nullLiteralOption struct{ literal string }

// nullLiteralOf returns the erase literal declared for a field, if any.
func nullLiteralOf(f *figureout.FieldModel) (string, bool) {
	p, ok := f.Source(Source)
	if !ok {
		return "", false
	}
	for _, o := range p.Options {
		if n, ok := o.(nullLiteralOption); ok {
			return n.literal, true
		}
	}
	return "", false
}

// separatorOf returns the list separator declared for a field, if any.
func separatorOf(f *figureout.FieldModel) string {
	p, ok := f.Source(Source)
	if !ok {
		return defaultSeparator
	}
	for _, o := range p.Options {
		if s, ok := o.(separatorOption); ok {
			return s.sep
		}
	}
	return defaultSeparator
}
