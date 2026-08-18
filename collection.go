package figureout

import (
	"reflect"
	"unsafe"
)

// ListOf registers a list whose elements are configuration objects, described
// inline.
//
// A list of objects is the part of a configuration file an operator actually
// edits, and describing it is what lets its elements have names, defaults,
// constraints, provenance and a schema:
//
//	figureout.ListOf(s, &c.Sites, "sites", func(e *Site, s *figureout.Schema[Site]) {
//		figureout.Explicit(s, &e.Name, "name").NonEmpty()
//		figureout.Value(s, &e.MaxBytes, "max_bytes").ApplyDefault(0)
//	})
//
// Each element binds to its own path — "sites[0].max_bytes" — so merging,
// erasure and [Report.OriginOf] all work on an element the way they work on any
// other field. See [ListField.MergeByKey] to identify elements by one of their
// own fields instead of by position.
func ListOf[R, E any](
	s *Schema[R],
	field *[]E,
	name string,
	describe func(*E, *Schema[E]),
	opts ...FieldOption,
) *ListField {
	return registerList(s, field, name, elementModel(s.b, name, describe), opts)
}

// List registers a list whose elements are described by their own descriptor.
//
// It is [ListOf] for an element description shared by several parents, or one
// an adopter wants to export.
func List[R, E any](
	s *Schema[R],
	field *[]E,
	name string,
	d *Descriptor[E],
	opts ...FieldOption,
) *ListField {
	return registerList(s, field, name, descriptorModel(s.b, name, d), opts)
}

// MapOf registers a map whose values are configuration objects, described
// inline. The map key identifies an element, so "proxies[gitlab].url" names one
// entry.
// The pointer is the binding identity: figureout resolves a registration by
// the address of the field inside the synthetic root, so it cannot take the map
// itself.
//
//nolint:gocritic // ptrToRefParam: see above
func MapOf[R any, K comparable, E any](
	s *Schema[R],
	field *map[K]E,
	name string,
	describe func(*E, *Schema[E]),
	opts ...FieldOption,
) *MapField {
	f := registerCollection(s, unsafe.Pointer(field), reflect.TypeFor[map[K]E](), name,
		elementModel(s.b, name, describe), opts)
	return &MapField{f}
}

// Map registers a map whose values are described by their own descriptor.
// The pointer is the binding identity: figureout resolves a registration by
// the address of the field inside the synthetic root, so it cannot take the map
// itself.
//
//nolint:gocritic // ptrToRefParam: see above
func Map[R any, K comparable, E any](
	s *Schema[R],
	field *map[K]E,
	name string,
	d *Descriptor[E],
	opts ...FieldOption,
) *MapField {
	f := registerCollection(s, unsafe.Pointer(field), reflect.TypeFor[map[K]E](), name,
		descriptorModel(s.b, name, d), opts)
	return &MapField{f}
}

func registerList[R, E any](
	s *Schema[R],
	field *[]E,
	name string,
	elem func(*registration) *ObjectModel,
	opts []FieldOption,
) *ListField {
	f := registerCollection(s, unsafe.Pointer(field), reflect.TypeFor[[]E](), name, elem, opts)
	return &ListField{f}
}

// registerCollection records a list or map field and attaches an element model
// to its semantic type.
func registerCollection[R any](
	s *Schema[R],
	ptr unsafe.Pointer,
	carrier reflect.Type,
	name string,
	elem func(*registration) *ObjectModel,
	opts []FieldOption,
) *FieldBuilder {
	b := s.b
	reg := b.register(ptr, carrier, name, regField)
	if reg.valid {
		switch {
		case reg.acc.presence != PresenceRequired:
			b.diags.errorf(CodeUnsupportedType, reg.goName, name,
				"collections of objects do not support %s presence yet", reg.acc.presence)
		case reg.typ.Elem == nil:
			b.diags.errorf(CodeUnsupportedType, reg.goName, name,
				"%s is not a list or a map", reg.acc.elem)
		default:
			if model := elem(reg); model != nil {
				// Type.Elem is shared with the derived type, so replace it
				// rather than mutating what deriveType returned.
				next := *reg.typ.Elem
				next.Object = model
				reg.typ.Elem = &next
			}
		}
	}
	b.applyOptions(reg, opts)
	return &FieldBuilder{b: b, reg: reg}
}

// elementModel compiles an element description against a standalone zero
// element, so pointers taken inside describe bind to E rather than to the
// configuration root.
func elementModel[E any](b *builder, name string, describe func(*E, *Schema[E])) func(*registration) *ObjectModel {
	return func(reg *registration) *ObjectModel {
		if describe == nil {
			b.diags.errorf(CodeMissingDefinition, reg.goName, name,
				"nil describe function for the elements of %q", name)
			return nil
		}
		root := new(E)
		rv := reflect.ValueOf(root).Elem()
		if rv.Kind() != reflect.Struct {
			b.diags.errorf(CodeUnsupportedType, reg.goName, name,
				"elements of %q must be structs, got %s", name, rv.Type())
			return nil
		}

		obj, nb := openObject(b, rv, ElementPath(reg.goName, ""), describe)
		if nb == nil {
			// The element description encloses this list: the elements are of
			// the very shape being described. A collection is where recursion
			// ends by itself, since a layer that provides no elements provides
			// no level either.
			return obj
		}
		b.diags = append(b.diags, nb.diags...)
		if len(nb.invariants) > 0 {
			// An element invariant would have to run per element, with paths to
			// match; until it does, saying so beats running it once or never.
			b.diags.errorf(CodeMissingDefinition, reg.goName, name,
				"an invariant cannot be registered on the elements of %q; "+
					"declare it on the configuration that owns the list", name)
		}
		return obj
	}
}

func descriptorModel[E any](b *builder, name string, d *Descriptor[E]) func(*registration) *ObjectModel {
	return func(reg *registration) *ObjectModel {
		if d == nil {
			b.diags.errorf(CodeMissingDefinition, reg.goName, name,
				"nil descriptor for the elements of %q", name)
			return nil
		}
		return d.model.Root
	}
}

// ListField is the fluent builder for a list of objects.
type ListField struct{ *FieldBuilder }

// Doc attaches documentation.
func (f *ListField) Doc(text string) *ListField {
	f.FieldBuilder.Doc(text)
	return f
}

// Required makes an absent list an error instead of an empty one.
func (f *ListField) Required() *ListField {
	f.FieldBuilder.Required()
	return f
}

// MergeReplace takes the list from the last layer that provided one. It is the
// default.
func (f *ListField) MergeReplace() *ListField {
	if f.ok() {
		f.reg.merge = MergeReplace
		f.reg.mergeKey = ""
	}
	return f
}

// MergeAppend concatenates the elements of every layer, in layer order.
func (f *ListField) MergeAppend() *ListField {
	if f.ok() {
		f.reg.merge = MergeAppend
		f.reg.mergeKey = ""
	}
	return f
}

// MergeByKey identifies elements by one of their own fields, so a later layer
// edits an element rather than restating the list.
//
//	figureout.ListOf(s, &c.Sites, "sites", describeSite).MergeByKey("name")
//
//	# base.yaml            # override.yaml        # result
//	sites:                 sites:                 sites:
//	  - name: docs           - name: docs           - name: docs
//	    max_bytes: 10            max_bytes: 20          max_bytes: 20
//	  - name: wiki                                  - name: wiki
//	    max_bytes: 10                                   max_bytes: 10
//
// Elements then bind to "sites[name=docs].max_bytes", which is a stable
// identity across layers where a position is not: prepending one element would
// otherwise re-target every override silently. Fields merge individually, so a
// later layer changes only what it names, and "sites[name=docs]: null" removes
// the element outright.
//
// The key field becomes mandatory in every element, and repeating it within one
// layer is an error rather than last-wins. Base order is preserved and unseen
// keys are appended, so a later layer cannot reorder: do not key a list whose
// order is meaningful.
func (f *ListField) MergeByKey(field string) *ListField {
	if !f.ok() {
		return f
	}
	if field == "" {
		f.b.diags.errorf(CodeConstraintMismatch, f.reg.goName, f.reg.name,
			"empty merge key for %q", f.reg.name)
		return f
	}
	f.reg.merge = MergeByKey
	f.reg.mergeKey = field
	return f
}

// MapField is the fluent builder for a map of objects.
type MapField struct{ *FieldBuilder }

// Doc attaches documentation.
func (f *MapField) Doc(text string) *MapField {
	f.FieldBuilder.Doc(text)
	return f
}

// Required makes an absent map an error instead of an empty one.
func (f *MapField) Required() *MapField {
	f.FieldBuilder.Required()
	return f
}

// MergeReplace takes the map from the last layer that provided one. It is the
// default, as it is everywhere else: a predictable last-one-wins is what a
// reader of a layered configuration can reason about.
func (f *MapField) MergeReplace() *MapField {
	if f.ok() {
		f.reg.merge = MergeReplace
	}
	return f
}

// MergeByKey merges entries across layers, so a later layer changes only the
// entries it names, and only the fields it names within them.
//
//	figureout.MapOf(s, &c.Proxies, "proxies", describeProxy).MergeByKey()
//
// An entry already identifies itself, so no key has to be named. Setting an
// entry to null removes it.
func (f *MapField) MergeByKey() *MapField {
	if f.ok() {
		f.reg.merge = MergeByKey
	}
	return f
}

// resolveMergeKey binds a list's declared merge key to the element field it
// names, once the element model is compiled.
func (b *builder) resolveMergeKey(f *FieldModel, key string) {
	elem, isCollection := collectionOf(f)
	if !isCollection {
		b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"a merge key applies to a list of objects, not to a %s", f.Type.Kind)
		return
	}
	if f.Type.Kind != TypeList {
		// A map already has a key; naming a second one is a contradiction.
		b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"a map is already keyed; %q cannot name another key", key)
		return
	}

	// byName is built when the model is indexed, which is after compilation.
	target := member(elem, key)
	switch {
	case target == nil:
		b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"merge key %q is not a field of the elements of %q", key, f.Name)
	case target.Presence != PresenceRequired:
		b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"merge key %q must be required: it identifies an element", key)
	case !scalarKind(target.Type.Kind):
		b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"merge key %q must be a scalar, not a %s", key, target.Type.Kind)
	default:
		f.mergeKey = target
	}
}

func scalarKind(k TypeKind) bool {
	switch k {
	case TypeBoolean, TypeInteger, TypeNumber, TypeString, TypeDuration, TypeTimestamp:
		return true
	default:
		return false
	}
}
