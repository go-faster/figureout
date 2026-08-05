package figureout

import (
	"reflect"

	"github.com/go-faster/errors"
)

// FieldID identifies a field within one compiled [Descriptor].
type FieldID uint32

// Metadata is documentation attached to a field.
type Metadata struct {
	Doc        string
	Deprecated string
	Hidden     bool
	// Secret marks a credential. Unlike Hidden it is enforced: see [Secret].
	Secret   bool
	Examples []any
}

// Default is a field default.
//
// Applied defaults change the resolved value; documented defaults only
// contribute metadata. The distinction matters for [OptionalOf], where applying
// a default turns a missing value into a present one.
type Default struct {
	Value   any
	Applied bool
}

// ObjectModel is a compiled configuration object.
type ObjectModel struct {
	// Go is the struct type described.
	Go     reflect.Type
	Fields []*FieldModel

	byName map[string]*FieldModel
}

// Field looks up a field by its canonical name within the object.
func (o *ObjectModel) Field(name string) (*FieldModel, bool) {
	f, ok := o.byName[name]
	return f, ok
}

func (o *ObjectModel) index() {
	o.byName = make(map[string]*FieldModel, len(o.Fields))
	for _, f := range o.Fields {
		o.byName[f.Name] = f
	}
}

// FieldModel is a compiled configuration field.
type FieldModel struct {
	ID FieldID
	// Name is the canonical name within the declaring object.
	Name string
	// Path is the canonical dotted path from the descriptor root.
	Path string
	// GoPath locates the Go field relative to the declaring object.
	GoPath FieldPath
	// GoName is the Go path for diagnostics, such as "Config.Server.Port".
	GoName string

	Type     Type
	Presence Presence

	Meta        Metadata
	Default     *Default
	Constraints []Constraint
	// Merge decides how the field combines values from several layers.
	Merge MergePolicy

	Sources map[SourceID]*SourceProjection
	Targets map[TargetID][]any

	// MovedFrom lists the former paths of the field, relative to the
	// descriptor that declares it. Each appears in the model as a deprecated
	// shadow field carrying [FieldModel.MovedTo].
	MovedFrom []string
	// MovedTo is the canonical path superseding this field, set on the shadow
	// fields [MovedFrom] creates. It is empty for a field of its own.
	MovedTo string

	// movedTo is the shadowed field. It is the identity behind MovedTo, which
	// is only a path.
	movedTo *FieldModel

	acc accessor
}

// Moved reports whether the field is a deprecated former spelling of another
// one. A moved field is never materialized: its value is redirected to
// [FieldModel.MovedTo] during resolution.
func (f *FieldModel) Moved() bool { return f.movedTo != nil }

// Source returns the projection of the field for the given source.
func (f *FieldModel) Source(id SourceID) (*SourceProjection, bool) {
	p, ok := f.Sources[id]
	return p, ok
}

// Validate runs every declarative and opaque constraint against v.
func (f *FieldModel) Validate(v any) error {
	for _, c := range f.Constraints {
		if err := c.Validate(v); err != nil {
			return err
		}
	}
	return nil
}

// Model is the compiled, format-neutral descriptor model.
//
// It contains no JSON Schema, CUE or wire types: emitters project it.
type Model struct {
	Root *ObjectModel

	fields     []*FieldModel
	byPath     map[string]*FieldModel
	invariants []InvariantModel
}

// Fields returns every field in the model, including nested ones, in
// declaration order.
func (m *Model) Fields() []*FieldModel { return m.fields }

// FieldByPath looks up a field by canonical dotted path.
func (m *Model) FieldByPath(path string) (*FieldModel, bool) {
	f, ok := m.byPath[path]
	return f, ok
}

func (m *Model) reindex() {
	m.fields = nil
	m.byPath = map[string]*FieldModel{}
	var walk func(o *ObjectModel)
	walk = func(o *ObjectModel) {
		o.index()
		for _, f := range o.Fields {
			f.ID = FieldID(len(m.fields))
			m.fields = append(m.fields, f)
			m.byPath[f.Path] = f
			if f.movedTo != nil {
				// Paths are assigned by the time reindex runs, so this is where
				// a shadow can finally name what superseded it.
				f.MovedTo = f.movedTo.Path
				if f.Meta.Deprecated == "" {
					f.Meta.Deprecated = "moved to " + f.MovedTo
				}
			}
			switch {
			case f.Type.Object != nil:
				walk(f.Type.Object)
			case f.Type.Union != nil:
				for _, v := range f.Type.Union.Variants {
					walk(v.Object)
				}
			}
		}
	}
	walk(m.Root)
}

// Descriptor is an immutable compiled configuration description.
//
// It is safe for concurrent use. Build one with [Derive] or [MustDerive].
type Descriptor[T any] struct {
	model      *Model
	invariants []invariant
}

// Model returns the compiled model. The returned value must not be mutated.
func (d *Descriptor[T]) Model() *Model { return d.model }

// accessor reads and writes one Go field relative to its declaring object.
//
// Carrier fields such as [OptionalOf] are written through the carrier interface,
// so presence is preserved rather than collapsed into a zero value.
type accessor struct {
	index    []int
	presence Presence
	// elem is the Go type of the carried value.
	elem reflect.Type
	// settable is false for unexported fields, which may only be ignored.
	settable bool
}

// field returns the addressable Go field within obj.
func (a accessor) field(obj reflect.Value) (reflect.Value, error) {
	if !a.settable {
		return reflect.Value{}, errors.New("field is unexported and cannot be assigned")
	}
	return obj.FieldByIndex(a.index), nil
}

func (a accessor) set(obj reflect.Value, v any) error {
	fv, err := a.field(obj)
	if err != nil {
		return err
	}
	if a.presence != PresenceRequired {
		return fv.Addr().Interface().(carrierRef).carrierSet(v)
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return errors.Errorf("cannot assign nil to %s", fv.Type())
	}
	switch {
	case rv.Type().AssignableTo(fv.Type()):
		fv.Set(rv)
	case rv.Type().ConvertibleTo(fv.Type()):
		fv.Set(rv.Convert(fv.Type()))
	default:
		return errors.Errorf("cannot assign %s to %s", rv.Type(), fv.Type())
	}
	return nil
}

func (a accessor) get(obj reflect.Value) (any, bool) {
	fv := obj.FieldByIndex(a.index)
	if a.presence != PresenceRequired {
		return fv.Addr().Interface().(carrierRef).carrierGet()
	}
	return fv.Interface(), true
}
