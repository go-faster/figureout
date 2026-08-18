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

	// widen turns the scalar spelling of an object into the object, for a
	// field registered with [ScalarOr].
	widen func(any) (any, error)

	// mergeKey is the element field identifying a list element across layers,
	// for a list registered with [ListField.MergeByKey].
	mergeKey *FieldModel

	// reason is why an opaque field is not described. See [FieldModel.Opaque].
	reason string

	// recursive is the object the field re-enters, when it closes a cycle in
	// the type graph. See [FieldModel.Recursive].
	recursive *ObjectModel

	// required records an explicit [FieldBuilder.Required].
	required bool

	// zeroDefault records that absence resolves to the zero value, which is
	// what [Value] declares and [Explicit] does not.
	zeroDefault bool

	acc accessor
}

// Required reports whether a source has to provide the field.
//
// A field registered with [Explicit] is required unless it carries an applied
// default, a collection included. A field registered with [Value] is not: its
// absence resolves to the zero value, and a collection to an empty one, because
// an absent list and an empty one are the same statement about the world. Both
// opt back in with [FieldBuilder.Required].
func (f *FieldModel) Required() bool {
	switch {
	case f.Presence != PresenceRequired:
		return false
	case f.Default != nil && f.Default.Applied:
		return false
	case f.zeroDefault:
		return false
	case f.Type.Kind == TypeList || f.Type.Kind == TypeMap:
		return f.required
	default:
		return true
	}
}

// ZeroDefault reports whether absence resolves to the zero value rather than to
// a diagnostic. It is what [Value] declares and [Explicit] withholds.
//
// A field with an applied [Default] never reports true: the default is what
// absence resolves to, and it is visible as one.
func (f *FieldModel) ZeroDefault() bool {
	return f.zeroDefault && (f.Default == nil || !f.Default.Applied)
}

// Recursive returns the configuration object the field re-enters, and reports
// whether it re-enters one at all.
//
// A configuration type may refer to itself: a node with a child node, a rule
// with nested rules. Every value is still a finite tree — recursion reaches a
// descriptor only through a pointer, a slice or a map, each of which may simply
// be absent — so the cycle exists in the type graph alone, and the model is a
// graph rather than an infinitely deep tree. The field closing the cycle points
// back at an object that already encloses it instead of at a copy of it.
//
// A target that names a shape emits the object once and refers to it. A target
// that spells one flat name per path, such as environment variables, has no
// bounded name for an unbounded path and stops here, the way it already stops
// at a collection of objects.
func (f *FieldModel) Recursive() (*ObjectModel, bool) {
	return f.recursive, f.recursive != nil
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
//
// A concrete element path resolves to the field describing every element, so
// "sites[0].max_bytes" and "sites[name=docs].max_bytes" both find
// "sites[].max_bytes".
func (m *Model) FieldByPath(path string) (*FieldModel, bool) {
	f, _, ok := m.fieldAt(path)
	return f, ok
}

// fieldAt is [Model.FieldByPath] with one more answer: whether the path names
// that field itself, rather than something the field only describes.
//
// The two differ under a former path, which is indexed against the models the
// moved field borrows: "old.list" finds the field describing the list without
// being the list's own path, and what a source writes there belongs to the old
// spelling until [Model.applyMoved] redirects it.
//
// A path the index does not hold is walked instead. A recursive descriptor has
// unboundedly many paths, so the index holds the shallowest spelling of each
// field and everything below one is found by following the shape it describes;
// a walk that arrives at a field arrives at the field itself.
func (m *Model) fieldAt(path string) (f *FieldModel, own, ok bool) {
	if f, ok := m.byPath[path]; ok {
		return f, f.Path == path, true
	}
	if canonical := CanonicalPath(path); canonical != path {
		if f, ok := m.byPath[canonical]; ok {
			return f, f.Path == canonical, true
		}
	}
	f, ok = m.Root.walkPath(path)
	return f, ok, ok
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
				// The shadow's structure is its target's, so it is indexed
				// under the former path rather than walked and re-pathed.
				m.indexShadow(f)
				continue
			}
			if f.recursive != nil {
				// The object is one that encloses this field, so it is indexed
				// where it is declared rather than again under every path that
				// re-enters it — and there are unboundedly many of those.
				continue
			}
			switch {
			case f.Type.Object != nil:
				walk(f.Type.Object)
			case f.Type.Union != nil:
				for _, v := range f.Type.Union.Variants {
					walk(v.Object)
				}
			}
			if elem, ok := collectionOf(f); ok {
				walk(elem)
			}
		}
	}
	walk(m.Root)
}

// indexShadow makes the members of a structured former path findable, without
// disturbing the models they are borrowed from.
//
// A shadow shares its target's object, union and element models, so its members
// resolve to the very same [FieldModel] values: a lookup under the old path
// finds the field that describes it, with the right merge policy and Go name.
func (m *Model) indexShadow(shadow *FieldModel) {
	var walk func(o *ObjectModel, prefix string)
	walk = func(o *ObjectModel, prefix string) {
		for _, f := range o.Fields {
			path := prefix + f.Name
			m.byPath[path] = f
			if f.recursive != nil {
				continue
			}
			switch {
			case f.Type.Object != nil:
				walk(f.Type.Object, path+".")
			case f.Type.Union != nil:
				for _, v := range f.Type.Union.Variants {
					walk(v.Object, path+".")
				}
			}
			if elem, ok := collectionOf(f); ok {
				walk(elem, ElementPath(path, "")+".")
			}
		}
	}

	switch {
	case shadow.Type.Object != nil:
		walk(shadow.Type.Object, shadow.Path+".")
	case shadow.Type.Union != nil:
		for _, v := range shadow.Type.Union.Variants {
			walk(v.Object, shadow.Path+".")
		}
	}
	if elem, ok := collectionOf(shadow); ok {
		walk(elem, ElementPath(shadow.Path, "")+".")
	}
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
	// indirect reports that the carrier holds a pointer to elem rather than an
	// elem, as "OptionalOf[*C]" does. It is orthogonal to presence: the carrier
	// alone says whether the value is there, and the pointer is allocated
	// whenever it is.
	indirect bool
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

	if a.indirect {
		// The value is held behind a pointer, so it is boxed before it goes in
		// — into a fresh one, so a resolved configuration aliases nothing a
		// source is still holding.
		boxed, err := boxPointer(a.elem, v)
		if err != nil {
			return err
		}
		v = boxed
	}

	if a.presence == PresenceOptional {
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

// boxPointer allocates a fresh *elem holding v.
func boxPointer(elem reflect.Type, v any) (any, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, errors.Errorf("cannot assign nil to *%s", elem)
	}
	out := reflect.New(elem)
	switch {
	case rv.Type().AssignableTo(elem):
		out.Elem().Set(rv)
	case rv.Type().ConvertibleTo(elem):
		out.Elem().Set(rv.Convert(elem))
	default:
		return nil, errors.Errorf("cannot assign %s to *%s", rv.Type(), elem)
	}
	return out.Interface(), nil
}

// carried is the Go type the carrier holds, which is a pointer to elem when the
// carrier is indirect.
// OptionalSection reports a nested object a source may leave out, whichever of
// the optional carriers spells it — an [OptionalOf], with or without a pointer
// inside it, or a bare pointer.
//
// A [Group] is never one. It nests the document without nesting the Go struct,
// so it has no field to hold "no section" in, and its members belong to
// whatever encloses it.
func (f *FieldModel) OptionalSection() bool {
	return f.Type.Object != nil && f.Presence != PresenceRequired && len(f.GoPath.Index) > 0
}

func (a accessor) carried() reflect.Type {
	if a.indirect {
		return reflect.PointerTo(a.elem)
	}
	return a.elem
}

// reach returns the addressable nested object inside obj, and whether there is
// one at all — an optional section nobody wrote has no members to reach.
func (a accessor) reach(obj reflect.Value) (reflect.Value, bool) {
	held := obj.FieldByIndex(a.index)
	if a.presence == PresenceOptional {
		ref := held.Addr().Interface().(carrierRef)
		if _, ok := ref.carrierGet(); !ok {
			return reflect.Value{}, false
		}
		held = reflect.NewAt(a.carried(), ref.carrierAddr()).Elem()
	}

	if !a.indirect {
		return held, true
	}

	// A pointer resolution wrote is never nil; one a hand-built value carries
	// may be, and reaching through it would panic.
	if held.IsNil() {
		return reflect.Value{}, false
	}
	return held.Elem(), true
}

// descend returns the nested object inside obj, allocating whatever indirection
// stands in the way. Both walks into a section that is there need it: a builder
// binds members by their address inside the object, and resolution writes them
// there, and neither has anything to address until the pointer holding it
// exists.
//
// It is [accessor.reach] for a section that is going to be there rather than
// one that may already be, which is why it allocates instead of reporting.
func (a accessor) descend(obj reflect.Value) reflect.Value {
	held := obj.FieldByIndex(a.index)
	if a.presence == PresenceOptional {
		held = reflect.NewAt(a.carried(), held.Addr().Interface().(carrierRef).carrierAddr()).Elem()
	}

	if !a.indirect {
		return held
	}

	held.Set(reflect.New(a.elem))
	return held.Elem()
}

// setZero writes the zero value of the field.
//
// It is not [accessor.set] with a zero: the zero value of an interface is an
// untyped nil, which no assignment can carry.
func (a accessor) setZero(obj reflect.Value) error {
	fv, err := a.field(obj)
	if err != nil {
		return err
	}
	fv.SetZero()
	return nil
}

func (a accessor) get(obj reflect.Value) (any, bool) {
	held := obj.FieldByIndex(a.index)
	if a.presence == PresenceOptional {
		v, ok := held.Addr().Interface().(carrierRef).carrierGet()
		if !ok {
			return nil, false
		}
		if !a.indirect {
			return v, true
		}
		held = reflect.ValueOf(v)
	}

	if !a.indirect {
		return held.Interface(), true
	}

	if !held.IsValid() || held.IsNil() {
		return nil, false
	}
	return held.Elem().Interface(), true
}
