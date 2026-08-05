// Package env projects a configuration descriptor onto environment variables.
package env

import (
	"context"
	"os"
	"slices"
	"strings"
	"unicode"

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
	naming Naming
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

		v, err := decode(e.field, raw, e.typ, separatorOf(e.field))
		if err != nil {
			layer.Diagnostics = append(layer.Diagnostics, figureout.Diagnostic{
				Severity:  figureout.SeverityError,
				Code:      figureout.CodeSourceUnsupported,
				Message:   figureout.Redact(e.field, err.Error(), raw),
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

// decode converts a variable's text, through the field's own decoder when it
// installed one. A decoder receives the raw text: an environment variable has no
// structure to present it with.
func decode(f *figureout.FieldModel, raw string, typ figureout.Type, sep string) (any, error) {
	if p, ok := f.Source(Source); ok && p.Decoder != nil {
		return p.Decoder.DecodeValue(raw)
	}
	return parse(typ, raw, sep)
}

// lookup returns the first variable that is set, with its name and value.
func (s *source) lookup(names []string) (name, value string, ok bool) {
	for _, n := range names {
		if v, set := s.vars[n]; set {
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
	// typ is the semantic type to parse the text as. It differs from the
	// field's own type for a ScalarOr shorthand.
	typ figureout.Type
	// discriminator marks a union tag rather than a semantic value.
	discriminator bool
}

// plan derives variable names for every field and checks for collisions.
//
// The walk is hierarchical rather than a pass over the flat field list,
// because a name is relative to the object that declares it: a nested
// descriptor contributes its own segment, and env.Name replaces only that
// segment. A flat pass would let a nested field silently claim the same
// variable as a top-level one.
func (s *source) plan(m *figureout.Model) ([]entry, figureout.Diagnostics) {
	p := &planner{source: s, byName: map[string]string{}}
	p.object(m.Root, nil)
	return p.out, p.diags
}

type planner struct {
	source *source
	out    []entry
	diags  figureout.Diagnostics
	byName map[string]string
}

func (p *planner) object(obj *figureout.ObjectModel, segments []string) {
	for _, f := range obj.Fields {
		if proj, ok := f.Source(Source); ok && proj.Skip {
			continue
		}
		own := append(slices.Clone(segments), segmentOf(f))

		switch {
		case f.Type.Union != nil:
			path, _ := figureout.DiscriminatorPath(f)
			p.add(entry{
				path:          path,
				names:         p.source.names(f, append(slices.Clone(own), f.Type.Union.Discriminator)),
				field:         f,
				typ:           f.Type,
				discriminator: true,
			})
			// A variant's members are siblings of the tag, so they share the
			// union's segments rather than nesting under the tag.
			for _, variant := range f.Type.Union.Variants {
				p.object(variant.Object, own)
			}
		case f.Moved():
			// A former *file* key does not imply a former variable. The name
			// derived from an old path is usually the very name the target
			// derives — "database_dsn" and "database.dsn" are one variable by
			// construction — so binding both would collide by design. Use
			// Alias when a variable really did exist under an old name.
			continue
		case isCollection(f):
			// A list or map of objects has no spelling here. An index
			// convention would be a second, worse way to write the same
			// configuration, so the field is simply not read from this source.
			continue
		case f.Type.Object != nil:
			// An object has no spelling here, so a ScalarOr field binds its
			// scalar alternative at the object's own name; its members still
			// bind under it.
			if scalar, ok := f.Shorthand(); ok {
				p.add(entry{path: f.Path, names: p.source.names(f, own), field: f, typ: scalar})
			}
			p.object(f.Type.Object, own)
		default:
			p.add(entry{path: f.Path, names: p.source.names(f, own), field: f, typ: f.Type})
		}
	}
}

func (p *planner) add(e entry) {
	for _, n := range e.names {
		if prev, ok := p.byName[n]; ok {
			p.diags = append(p.diags, figureout.Diagnostic{
				Severity: figureout.SeverityError,
				Code:     figureout.CodeSourceNameCollision,
				Source:   Source,
				Message:  "environment variable " + n + " is assigned to both " + prev + " and " + e.path,
			})
			return
		}
		p.byName[n] = e.path
	}
	p.out = append(p.out, e)
}

// isCollection reports whether a field is a list or map of described objects,
// which this source cannot express.
func isCollection(f *figureout.FieldModel) bool {
	_, ok := f.Elements()
	return ok
}

// segmentOf returns the field's own name segment, which [Name] replaces.
func segmentOf(f *figureout.FieldModel) string {
	if p, ok := f.Source(Source); ok && len(p.Names) > 0 {
		return p.Names[0]
	}
	return f.Name
}

// names returns the accepted variable names, primary first, aliases after.
func (s *source) names(f *figureout.FieldModel, segments []string) []string {
	naming := s.naming
	if naming == nil {
		naming = DefaultNaming
	}

	primary := naming(f, segments)
	var aliases []string
	if p, ok := f.Source(Source); ok {
		for _, o := range p.Options {
			if a, ok := o.(aliasOption); ok {
				aliases = append(aliases, a.names...)
			}
		}
	}

	out := make([]string, 0, len(primary)+len(aliases))
	for _, n := range append(primary, aliases...) {
		out = append(out, s.prefix+n)
	}
	return out
}

// Naming derives the unprefixed variable names for a field.
//
// segments is the path from the descriptor root to the field, one entry per
// level, with any [Name] override already applied. The first name returned is
// the primary one; the rest are tried in order. The source prepends the
// descriptor prefix and appends any [Alias] afterwards.
type Naming func(f *figureout.FieldModel, segments []string) []string

// DefaultNaming joins the segments with underscores and upper-cases the
// result, so "server.listenPort" becomes "SERVER_LISTEN_PORT".
func DefaultNaming(_ *figureout.FieldModel, segments []string) []string {
	return []string{derive(strings.Join(segments, "."))}
}

// derive converts a canonical path to an environment variable name:
// "server.listenPort" becomes "SERVER_LISTEN_PORT".
//
// Only camelCase boundaries split, so a name that is already upper case, such
// as one given to [Name], survives intact.
func derive(path string) string {
	runes := []rune(path)
	var sb strings.Builder
	for i, r := range runes {
		switch {
		case r == '.' || r == '-' || r == ' ':
			sb.WriteByte('_')
		case unicode.IsUpper(r) && i > 0 && boundary(runes, i):
			sb.WriteByte('_')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	return strings.ToUpper(sb.String())
}

// boundary reports whether an upper case rune starts a new word: either the
// previous rune is lower case, or it ends a run of upper case ones.
func boundary(runes []rune, i int) bool {
	prev := runes[i-1]
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
}

// Names replaces how variable names are derived.
//
//	env.Current(env.Names(func(f *figureout.FieldModel, segments []string) []string {
//		return []string{strings.ToUpper(strings.Join(segments, "__"))}
//	}))
func Names(n Naming) Option {
	return optionFunc(func(s *source) error {
		if n == nil {
			return errors.New("nil naming")
		}
		s.naming = n
		return nil
	})
}

// Name replaces the field's own name segment.
//
// The name is relative to the object that declares the field, so a nested
// descriptor keeps its parent's segments: Name("listen_port") on a field of a
// Server nested under "server" reads SERVER_LISTEN_PORT, not LISTEN_PORT. The
// descriptor prefix still applies.
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
