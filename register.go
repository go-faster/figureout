package figureout

import (
	"reflect"
	"unsafe"
)

// registerValue records a value field and returns its typed builder.
func registerValue[R, T any](
	s *Schema[R],
	ptr unsafe.Pointer,
	carrier reflect.Type,
	name string,
	opts []FieldOption,
) *ValueField[T] {
	b := s.b
	reg := b.register(ptr, carrier, name, regField)
	b.applyOptions(reg, opts)
	return &ValueField[T]{&FieldBuilder{b: b, reg: reg}}
}

// Value registers a plain field whose absence resolves to the zero value of T.
//
// The semantic type is derived from T, so named types such as
// "type Port uint16" are integers with whatever the type registry adds.
//
// Absence is not an error: a field nobody configured reads as "", 0 or false,
// which is what an optional scalar with no meaningful default wants, and the
// zero value is already visible in the Go type. Say so differently when it is
// not what the field means: [Explicit] demands a value, [ValueField.ApplyDefault]
// substitutes another one, and [Optional] keeps absence visible to the consumer.
//
// A zero that no source could have written is a compilation error rather than a
// silent one, so a field constrained by NonEmpty, InRange or Enum cannot fall
// back to it.
func Value[R, T any](s *Schema[R], field *T, name string, opts ...FieldOption) *ValueField[T] {
	f := registerPlain(s, field, name, opts)
	if f.ok() {
		f.reg.zeroDefault = true
	}
	return f
}

// Explicit registers a plain field some source has to provide.
//
// It is [Value] without the zero fallback: a missing value is an error unless
// the field carries an applied default. Use it for what an operator has to
// decide, such as a database address or a listen port.
//
// A collection is required too, rather than resolving to an empty one: the
// absent-is-empty rule is what a collection does when nobody says otherwise,
// and Explicit says otherwise.
func Explicit[R, T any](s *Schema[R], field *T, name string, opts ...FieldOption) *ValueField[T] {
	f := registerPlain(s, field, name, opts)
	if f.ok() {
		f.reg.required = true
	}
	return f
}

// registerPlain records a field bound to a plain Go value, which is every
// presence but a carrier's.
func registerPlain[R, T any](s *Schema[R], field *T, name string, opts []FieldOption) *ValueField[T] {
	b := s.b
	f := registerValue[R, T](s, unsafe.Pointer(field), reflect.TypeFor[T](), name, opts)
	if f.ok() && f.reg.acc.presence != PresenceRequired {
		b.diags.errorf(CodeUnsupportedType, f.reg.goName, name,
			"%s carries %s presence; register it with Optional",
			f.reg.goName, f.reg.acc.presence)
	}
	return f
}

// Optional registers a field that a source may leave out.
//
// The element type is inferred from the carrier, so the builder and its
// constraints are typed as T rather than as OptionalOf[T].
func Optional[R, T any](s *Schema[R], field *OptionalOf[T], name string, opts ...FieldOption) *ValueField[T] {
	return registerValue[R, T](s, unsafe.Pointer(field), reflect.TypeFor[OptionalOf[T]](), name, opts)
}

// Object registers a nested configuration object described by its own
// descriptor.
func Object[R, C any](s *Schema[R], field *C, name string, d *Descriptor[C], opts ...FieldOption) *ObjectField {
	b := s.b
	reg := b.register(unsafe.Pointer(field), reflect.TypeFor[C](), name, regObject)
	if reg.valid {
		switch {
		case d == nil:
			b.diags.errorf(CodeMissingDefinition, reg.goName, name, "nil descriptor for nested object %q", name)
		case reg.acc.presence != PresenceRequired:
			b.diags.errorf(CodeUnsupportedType, reg.goName, name,
				"nested objects do not support %s presence yet", reg.acc.presence)
		default:
			reg.object = d.model.Root
			reg.typ = Type{Kind: TypeObject, Go: reg.acc.elem, Object: d.model.Root}
		}
	}
	b.applyOptions(reg, opts)
	return &ObjectField{&FieldBuilder{b: b, reg: reg}}
}

// ObjectFunc registers a nested configuration object described inline.
//
// It is [Object] without a descriptor variable: describe runs against a nested
// [Schema] rooted at the field, so pointer binding, completeness and name
// collisions are scoped to C exactly as they would be in a separate [Derive].
//
//	figureout.ObjectFunc(s, &c.Server, "server", func(c *Server, s *figureout.Schema[Server]) {
//		figureout.Explicit(s, &c.Port, "port").InRange(1, 65535)
//	})
//
// Prefer [Object] for a descriptor shared by several parents or exported for
// its own sake, and ObjectFunc for a section that has exactly one parent.
func ObjectFunc[R, C any](
	s *Schema[R],
	field *C,
	name string,
	describe func(*C, *Schema[C]),
	opts ...FieldOption,
) *ObjectField {
	b := s.b
	reg := b.register(unsafe.Pointer(field), reflect.TypeFor[C](), name, regObject)
	if reg.valid {
		switch {
		case describe == nil:
			b.diags.errorf(CodeMissingDefinition, reg.goName, name,
				"nil describe function for nested object %q", name)
		case reg.acc.presence != PresenceRequired:
			b.diags.errorf(CodeUnsupportedType, reg.goName, name,
				"nested objects do not support %s presence yet", reg.acc.presence)
		default:
			reg.object = describeNested(b, reg, describe)
			reg.typ = Type{Kind: TypeObject, Go: reg.acc.elem, Object: reg.object}
		}
	}
	b.applyOptions(reg, opts)
	return &ObjectField{&FieldBuilder{b: b, reg: reg}}
}

// describeNested compiles a child object with its own builder, rooted at the
// nested value inside this builder's synthetic object.
func describeNested[C any](b *builder, reg *registration, describe func(*C, *Schema[C])) *ObjectModel {
	rv := b.root.FieldByIndex(reg.bound.index)
	nb := newBuilder(rv, reg.goName, b.opts)
	describe(rv.Addr().Interface().(*C), &Schema[C]{b: nb})
	obj := nb.compile()
	b.diags = append(b.diags, nb.diags...)
	b.invariants = append(b.invariants, lift(nb.invariants, reg.name, reg.bound.index)...)
	return obj
}

// IgnoreOption customizes an ignore declaration.
type IgnoreOption interface {
	applyIgnore(*registration) error
}

type ignoreOptionFunc func(*registration) error

func (f ignoreOptionFunc) applyIgnore(r *registration) error { return f(r) }

// Reason documents why a field is ignored.
func Reason(text string) IgnoreOption {
	return ignoreOptionFunc(func(r *registration) error {
		r.reason = text
		return nil
	})
}

// Ignore marks a field as deliberately not part of the configuration.
//
// Only the field itself is ignored; nested fields still have to be accounted
// for. Use [IgnoreRecursive] to ignore a whole subtree.
func Ignore[R, C any](s *Schema[R], field *C, opts ...IgnoreOption) {
	ignore(s, unsafe.Pointer(field), reflect.TypeFor[C](), false, opts)
}

// IgnoreRecursive marks a field and every field below it as deliberately not
// part of the configuration.
func IgnoreRecursive[R, C any](s *Schema[R], field *C, opts ...IgnoreOption) {
	ignore(s, unsafe.Pointer(field), reflect.TypeFor[C](), true, opts)
}

func ignore[R any](s *Schema[R], ptr unsafe.Pointer, carrier reflect.Type, recursive bool, opts []IgnoreOption) {
	b := s.b
	reg := b.register(ptr, carrier, "", regIgnore)
	reg.recursive = recursive
	for _, o := range opts {
		if err := o.applyIgnore(reg); err != nil {
			b.diags.errorf(CodeMissingDefinition, reg.goName, "", "ignore option: %s", err)
		}
	}
}

// IgnorePath ignores a field by Go path, such as "Server.Marker".
//
// Distinct zero-sized fields share an address, so pointer identity cannot
// select between them; path-based ignores are the supported way to handle them.
func IgnorePath[R any](s *Schema[R], path string, opts ...IgnoreOption) {
	b := s.b
	bd, err := b.bind.lookupPath(path)
	if err != nil {
		b.diags.errorf(CodeMissingDefinition, path, "", "%s", err)
		return
	}

	reg := &registration{
		kind:   regIgnore,
		valid:  true,
		bound:  bd,
		goName: bd.goPath,
		acc:    accessor{index: bd.index, settable: !bd.skipped},
	}
	reg.acc.presence, reg.acc.elem = unwrapCarrier(bd.typ)
	for _, o := range opts {
		if err := o.applyIgnore(reg); err != nil {
			b.diags.errorf(CodeMissingDefinition, reg.goName, "", "ignore option: %s", err)
		}
	}
	b.add(reg)
}

// IgnoreRecursivePath ignores a field and its subtree by Go path.
func IgnoreRecursivePath[R any](s *Schema[R], path string, opts ...IgnoreOption) {
	IgnorePath(s, path, append(opts, ignoreOptionFunc(func(r *registration) error {
		r.recursive = true
		return nil
	}))...)
}
