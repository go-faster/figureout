// Package file reads configuration values from a directory of files, one value
// per file.
//
// It is the shape a Kubernetes secret mount, a Docker secret and systemd's
// LoadCredential all present: a directory whose entries are named after the
// values they hold. Reading them is the half of secret handling the environment
// source cannot cover, because a mounted secret is a path, not a variable.
//
//	cfg, report, err := ConfigDescriptor.Resolve(
//		yaml.File("config.yaml"),
//		file.Dir("/run/secrets", file.Optional()),
//		env.Current(env.Prefix("APP_")),
//	)
//
// A value is the file's contents with one trailing newline removed, so that a
// secret written with "echo" reads back as written. A missing file leaves the
// field to earlier layers.
package file

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/scalar"
)

// Source identifies the file source.
const Source = figureout.SourceID("file")

// Option customizes the file source.
type Option interface {
	applyFile(*source) error
}

type optionFunc func(*source) error

func (f optionFunc) applyFile(s *source) error { return f(s) }

// Optional makes a missing directory produce an empty layer rather than an
// error. A mount that is absent in development is the usual reason.
func Optional() Option {
	return optionFunc(func(s *source) error {
		s.optional = true
		return nil
	})
}

// Names replaces how file names are derived. See [Naming].
func Names(n Naming) Option {
	return optionFunc(func(s *source) error {
		if n == nil {
			return errors.New("nil naming")
		}
		s.naming = n
		return nil
	})
}

// Naming derives the file names accepted for a field.
//
// segments is the path from the descriptor root to the field, one entry per
// level, with any [Name] override already applied. The first name returned is
// the primary one; the rest are tried in order.
type Naming func(f *figureout.FieldModel, segments []string) []string

// DefaultNaming joins the segments with dots, so "database.dsn" is read from a
// file named "database.dsn".
func DefaultNaming(_ *figureout.FieldModel, segments []string) []string {
	return []string{strings.Join(segments, ".")}
}

type source struct {
	dir      string
	optional bool
	naming   Naming
	fsys     fs.FS
}

// Dir reads values from the files in a directory.
func Dir(path string, opts ...Option) figureout.Source {
	return newSource(path, os.DirFS(path), opts)
}

// FS reads values from a filesystem. It is the hermetic form of [Dir],
// intended for tests and for embedding.
func FS(fsys fs.FS, opts ...Option) figureout.Source {
	return newSource("", fsys, opts)
}

func newSource(dir string, fsys fs.FS, opts []Option) figureout.Source {
	s := &source{dir: dir, fsys: fsys}
	for _, o := range opts {
		if err := o.applyFile(s); err != nil {
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

	if _, err := fs.Stat(s.fsys, "."); err != nil {
		if errors.Is(err, fs.ErrNotExist) && s.optional {
			return layer, nil
		}
		return nil, errors.Wrapf(err, "directory %q", s.dir)
	}

	plan, diags := s.plan(m)
	layer.Diagnostics = append(layer.Diagnostics, diags...)
	if layer.Diagnostics.HasErrors() {
		return layer, nil
	}

	for _, e := range plan {
		name, raw, ok, err := s.read(e.names)
		if err != nil {
			layer.Diagnostics = append(layer.Diagnostics, figureout.Diagnostic{
				Severity:  figureout.SeverityError,
				Code:      figureout.CodeSourceUnsupported,
				Message:   err.Error(),
				FieldPath: e.path,
				GoPath:    e.field.GoName,
				Source:    Source,
			})
			continue
		}
		if !ok {
			continue
		}
		origin := figureout.Origin{Source: Source, Name: name, File: filepath.Join(s.dir, name)}

		if e.discriminator {
			layer.Set(e.path, raw, origin)
			continue
		}

		v, err := scalar.ParseText(e.field.Type, raw, separatorOf(e.field))
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

// read returns the contents of the first file that exists.
func (s *source) read(names []string) (name, value string, ok bool, err error) {
	for _, n := range names {
		if !fs.ValidPath(n) {
			return "", "", false, errors.Errorf("%q is not a valid file name", n)
		}
		data, err := fs.ReadFile(s.fsys, n)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return "", "", false, errors.Wrapf(err, "read %q", n)
		}
		// A secret written with "echo" ends in a newline that is not part of it.
		return n, strings.TrimSuffix(string(data), "\n"), true, nil
	}
	return "", "", false, nil
}

// entry is one file binding.
type entry struct {
	path  string
	names []string
	field *figureout.FieldModel
	// discriminator marks a union tag rather than a semantic value.
	discriminator bool
}

// plan derives file names for every field and checks for collisions.
//
// The walk is hierarchical rather than a pass over the flat field list, for the
// same reason the environment source walks: a name is relative to the object
// that declares it, and [Name] replaces only that segment.
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
				discriminator: true,
			})
			for _, variant := range f.Type.Union.Variants {
				p.object(variant.Object, own)
			}
		case f.Type.Object != nil:
			p.object(f.Type.Object, own)
		default:
			p.add(entry{path: f.Path, names: p.source.names(f, own), field: f})
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
				Message:  "file " + n + " is assigned to both " + prev + " and " + e.path,
			})
			return
		}
		p.byName[n] = e.path
	}
	p.out = append(p.out, e)
}

// segmentOf returns the field's own name segment, which [Name] replaces.
func segmentOf(f *figureout.FieldModel) string {
	if p, ok := f.Source(Source); ok && len(p.Names) > 0 {
		return p.Names[0]
	}
	return f.Name
}

// names returns the accepted file names, primary first, aliases after.
func (s *source) names(f *figureout.FieldModel, segments []string) []string {
	naming := s.naming
	if naming == nil {
		naming = DefaultNaming
	}

	out := slices.Clone(naming(f, segments))
	if p, ok := f.Source(Source); ok {
		for _, o := range p.Options {
			if a, ok := o.(aliasOption); ok {
				out = append(out, a.names...)
			}
		}
	}
	return out
}

// Name replaces the field's own name segment.
//
// The name is relative to the object that declares the field, so a nested
// descriptor keeps its parent's segments, exactly as in the environment source.
func Name(name string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if name == "" {
			return errors.New("empty file name")
		}
		return c.SetSourceNames(Source, name)
	})
}

// Alias adds accepted file names, tried after the primary one.
func Alias(names ...string) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if len(names) == 0 {
			return errors.New("no aliases given")
		}
		return c.AddSourceOption(Source, aliasOption{names})
	})
}

type aliasOption struct{ names []string }

// Skip excludes a field from the file source.
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

func separatorOf(f *figureout.FieldModel) string {
	p, ok := f.Source(Source)
	if !ok {
		return scalar.DefaultSeparator
	}
	for _, o := range p.Options {
		if s, ok := o.(separatorOption); ok {
			return s.sep
		}
	}
	return scalar.DefaultSeparator
}
