package figureout

import (
	"time"

	"github.com/go-faster/errors"
)

// FieldOption customizes a field registration.
//
// Options are deliberately not generic over the field's value type: Go cannot
// infer a type argument for a nested call such as env.Name("PORT"), so a
// generic FieldOption[V] would force every option call site to spell the type.
// Value-typed operations live on the returned fluent builder instead.
type FieldOption interface {
	ApplyFieldOption(FieldOptionContext) error
}

// FieldOptionFunc adapts a function to [FieldOption].
type FieldOptionFunc func(FieldOptionContext) error

// ApplyFieldOption implements [FieldOption].
func (f FieldOptionFunc) ApplyFieldOption(c FieldOptionContext) error { return f(c) }

// FieldOptionContext is the controlled surface an option may mutate.
//
// It exposes registration methods rather than internal state, so adapter
// packages can extend the core without importing its internals.
type FieldOptionContext interface {
	// Name returns the canonical field name.
	Name() string
	// GoName returns the Go path of the field, for diagnostics.
	GoName() string
	// Type returns the semantic type of the field.
	Type() Type
	// Presence returns how the field models absence.
	Presence() Presence

	AddConstraint(Constraint) error
	AddMetadata(Metadata) error
	AddTargetAnnotation(TargetID, any) error
	// SetUnit scales bare numbers written for a duration field.
	SetUnit(time.Duration) error
	// AddMovedFrom records a former path of the field.
	AddMovedFrom(string) error
	// SetReason documents why a field is not described. It applies to an
	// opaque passthrough, which is the only field that is not.
	SetReason(string) error

	// SetSourceNames sets the primary name and aliases for a source.
	SetSourceNames(SourceID, ...string) error
	// AddSourceShapes declares the wire shapes a source accepts.
	AddSourceShapes(SourceID, ...Shape) error
	// SetSourceDecoder installs a decoder. A decoder without declared shapes
	// is reported by schema generation.
	SetSourceDecoder(SourceID, Decoder) error
	// SkipSource excludes the field from a source.
	SkipSource(SourceID) error
	// AddSourceOption attaches source-specific settings.
	AddSourceOption(SourceID, any) error
}

// fieldContext implements [FieldOptionContext] over a pending registration.
type fieldContext struct {
	reg *registration
}

func (c *fieldContext) Name() string       { return c.reg.name }
func (c *fieldContext) GoName() string     { return c.reg.goName }
func (c *fieldContext) Type() Type         { return c.reg.typ }
func (c *fieldContext) Presence() Presence { return c.reg.acc.presence }

func (c *fieldContext) AddConstraint(cs Constraint) error {
	if cs == nil {
		return errors.New("nil constraint")
	}
	c.reg.constraints = append(c.reg.constraints, cs)
	return nil
}

func (c *fieldContext) AddMetadata(m Metadata) error {
	if m.Doc != "" {
		c.reg.meta.Doc = m.Doc
	}
	if m.Deprecated != "" {
		c.reg.meta.Deprecated = m.Deprecated
	}
	if m.Hidden {
		c.reg.meta.Hidden = true
	}
	if m.Secret {
		c.reg.meta.Secret = true
	}
	c.reg.meta.Examples = append(c.reg.meta.Examples, m.Examples...)
	return nil
}

func (c *fieldContext) SetUnit(u time.Duration) error {
	if u <= 0 {
		return errors.New("unit must be positive")
	}
	if c.reg.typ.Kind != TypeDuration {
		return errors.Errorf("a unit applies to a duration field, not to a %s one", c.reg.typ.Kind)
	}
	c.reg.typ.Unit = u
	return nil
}

func (c *fieldContext) AddMovedFrom(path string) error {
	if path == "" {
		return errors.New("empty former path")
	}
	c.reg.movedFrom = append(c.reg.movedFrom, path)
	return nil
}

func (c *fieldContext) SetReason(text string) error {
	if text == "" {
		return errors.New("empty reason")
	}
	if c.reg.typ.Kind != TypeOpaque {
		return errors.Errorf("a reason says why a field is not described, and %q is a %s; "+
			"use Doc to document one that is", c.reg.name, c.reg.typ.Kind)
	}
	c.reg.reason = text
	return nil
}

func (c *fieldContext) AddTargetAnnotation(id TargetID, v any) error {
	if id == "" {
		return errors.New("empty target id")
	}
	if c.reg.targets == nil {
		c.reg.targets = map[TargetID][]any{}
	}
	c.reg.targets[id] = append(c.reg.targets[id], v)
	return nil
}

func (c *fieldContext) projection(id SourceID) (*SourceProjection, error) {
	if id == "" {
		return nil, errors.New("empty source id")
	}
	if c.reg.sources == nil {
		c.reg.sources = map[SourceID]*SourceProjection{}
	}
	p, ok := c.reg.sources[id]
	if !ok {
		p = &SourceProjection{Source: id}
		c.reg.sources[id] = p
	}
	return p, nil
}

func (c *fieldContext) SetSourceNames(id SourceID, names ...string) error {
	p, err := c.projection(id)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("no names given")
	}
	p.Names = append(p.Names, names...)
	return nil
}

func (c *fieldContext) AddSourceShapes(id SourceID, shapes ...Shape) error {
	p, err := c.projection(id)
	if err != nil {
		return err
	}
	p.Accepts = append(p.Accepts, shapes...)
	return nil
}

func (c *fieldContext) SetSourceDecoder(id SourceID, d Decoder) error {
	p, err := c.projection(id)
	if err != nil {
		return err
	}
	if p.Decoder != nil {
		return errors.Errorf("source %q already has a decoder", id)
	}
	p.Decoder = d
	return nil
}

func (c *fieldContext) SkipSource(id SourceID) error {
	p, err := c.projection(id)
	if err != nil {
		return err
	}
	p.Skip = true
	return nil
}

func (c *fieldContext) AddSourceOption(id SourceID, v any) error {
	p, err := c.projection(id)
	if err != nil {
		return err
	}
	p.Options = append(p.Options, v)
	return nil
}

// AcceptShapes declares the wire shapes a source accepts for a field.
//
// Source packages normally wrap this in their own option, such as
// json.Accepts. Declaring shapes is what lets schema generation describe a
// custom decoder that would otherwise be opaque.
func AcceptShapes(id SourceID, shapes ...Shape) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		if len(shapes) == 0 {
			return errors.New("no shapes given")
		}
		return c.AddSourceShapes(id, shapes...)
	})
}

// WithDecoder installs a source decoder together with the shapes it accepts.
//
// A decoder is opaque, so the shapes are mandatory: without them, schema
// generation cannot describe what the source will accept, and nothing decides
// what reaches the decoder. A shape the field did not declare is rejected
// before the decoder runs.
//
// The decoder owns every shape it declared, including object and array ones the
// semantic type cannot describe — which is what makes it the way to keep
// parsing a carrier that is going away:
//
//	figureout.Value(s, &c.Token, "token",
//		figureout.WithDecoder(yaml.Source, carrierDecoder{},
//			figureout.Shape{Kind: figureout.ShapeString},
//			figureout.Shape{Kind: figureout.ShapeObject, Fields: map[string]figureout.Shape{
//				"env": {Kind: figureout.ShapeString},
//			})))
//
// A tree source hands over the node as []any, map[string]any or the scalar it
// decoded; sources whose values are text, such as env and file, hand over the
// text. Null never reaches a decoder: it stays a merge directive that erases.
func WithDecoder(id SourceID, d Decoder, shapes ...Shape) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		if d == nil {
			return errors.New("nil decoder")
		}
		if len(shapes) == 0 {
			return errors.Errorf("decoder for source %q declares no accepted shapes", id)
		}
		if err := c.SetSourceDecoder(id, d); err != nil {
			return err
		}
		return c.AddSourceShapes(id, shapes...)
	})
}

// Unit lets a duration field be written as a bare number of u.
//
// Unit-suffixed integer keys outlive the configurations that introduced them,
// and moving one onto [time.Duration] normally means changing what the key
// accepts — 180 would have to become "180s", which breaks every deployment
// already running. A unit keeps the key and still resolves a [time.Duration]:
//
//	figureout.Value(s, &c.Timeout, "timeout_seconds", figureout.Unit(time.Second))
//
//	timeout_seconds: 180     // 180 * time.Second
//	timeout_seconds: "3m"    // still accepted, so a rename is a pure alias change
//
// Generated schemas describe the canonical form: an integer, with the unit
// named in the description.
func Unit(u time.Duration) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.SetUnit(u)
	})
}

// Doc attaches documentation to a field.
func Doc(text string) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddMetadata(Metadata{Doc: text})
	})
}

// MovedFrom accepts a former path of the field and reports its use.
//
// [Deprecated] is metadata: it says a key is going away without doing anything
// when the key is set. MovedFrom is the behavior a configuration actually needs
// while it is being reshaped:
//
//		figureout.Value(s, &c.HTTPAddr, "http_addr", figureout.MovedFrom("addr"))
//
//	  - the old spelling still resolves, with a [SeverityWarning] diagnostic in
//	    the [Report] naming both paths
//	  - setting both spellings is a [SeverityError], not a precedence rule: two
//	    spellings in one configuration are two intentions, and silently picking
//	    one is the worst available answer
//	  - the old path appears in generated schemas as a deprecated property
//
// The path is relative to the descriptor that declares the field, so it may
// name a former level: MovedFrom("legacy.addr") reads the old nesting. Levels
// that no longer exist are synthesized as deprecated objects; a level that is a
// nested descriptor of its own is reported rather than modified.
//
// That scope decides how to reshape a flat legacy key into a section, which is
// the main thing MovedFrom exists for. Use [Group], which keeps the field
// declared by the root schema, so a root-relative former path is in scope:
//
//	figureout.Group(s, "api", func(s *figureout.Schema[Config]) {
//		figureout.Value(s, &c.API.HTTPAddr, "http_addr",
//			figureout.MovedFrom("http_addr"))
//	})
//
// The same registration inside [ObjectFunc] cannot express it: the field is
// declared by the nested descriptor, where "http_addr" resolves to the field
// itself rather than to the document root, and is reported as such.
//
// A former path is a fact about documents, not about environment variables:
// sources that derive a name from the path skip a former one, because
// "database_dsn" and "database.dsn" derive the same variable and binding both
// would collide by construction. Where a variable really did exist under an old
// name, name it with that source's alias option.
func MovedFrom(paths ...string) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		if len(paths) == 0 {
			return errors.New("no former paths given")
		}
		for _, p := range paths {
			if err := c.AddMovedFrom(p); err != nil {
				return err
			}
		}
		return nil
	})
}

// Deprecated marks a field as deprecated with a reason.
//
// Setting a deprecated field is reported as a [SeverityWarning] diagnostic in
// the [Report]. To also accept a former spelling, use [MovedFrom].
func Deprecated(reason string) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddMetadata(Metadata{Deprecated: reason})
	})
}

// Hidden hides a field from generated documentation and help text.
func Hidden() FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddMetadata(Metadata{Hidden: true})
	})
}

// Examples attaches example values to a field.
func Examples(values ...any) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddMetadata(Metadata{Examples: values})
	})
}
