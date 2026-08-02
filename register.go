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

// Value registers a plain field: one that is always materialized.
//
// The semantic type is derived from T, so named types such as
// "type Port uint16" are integers with whatever the type registry adds. A
// missing value is an error unless the field has an applied default; use
// [Optional] for a field a source may leave out.
func Value[R, T any](s *Schema[R], field *T, name string, opts ...FieldOption) *ValueField[T] {
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
	if reg.goName != "" {
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
	b.regs = append(b.regs, reg)
}

// IgnoreRecursivePath ignores a field and its subtree by Go path.
func IgnoreRecursivePath[R any](s *Schema[R], path string, opts ...IgnoreOption) {
	IgnorePath(s, path, append(opts, ignoreOptionFunc(func(r *registration) error {
		r.recursive = true
		return nil
	}))...)
}
