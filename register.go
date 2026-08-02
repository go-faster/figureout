package figureout

import (
	"reflect"
	"unsafe"
)

// registerScalar records a scalar field and checks the derived semantic kind
// against the helper that was used.
func registerScalar[R any](
	s *Schema[R],
	ptr unsafe.Pointer,
	carrier reflect.Type,
	name string,
	want TypeKind,
	opts []FieldOption,
) *FieldBuilder {
	b := s.b
	reg := b.register(ptr, carrier, name, regField)
	if reg.goName != "" && reg.typ.Kind != TypeInvalid && reg.typ.Kind != want {
		b.diags.errorf(CodeUnsupportedType, reg.goName, name,
			"%s is a %s field, but was registered as %s", reg.goName, reg.typ.Kind, want)
	}
	b.applyOptions(reg, opts)
	return &FieldBuilder{b: b, reg: reg}
}

// Bool registers a boolean field.
func Bool[R any, C BoolCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *BoolField {
	return &BoolField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeBoolean, opts)}
}

// String registers a string field.
func String[R any, C StringCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *StringField {
	return &StringField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeString, opts)}
}

// Int registers an integer field. Named integer types such as
// "type Port uint16" are accepted.
func Int[R any, C IntCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *IntField {
	return &IntField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeInteger, opts)}
}

// Float registers a floating point field.
func Float[R any, C FloatCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *FloatField {
	return &FloatField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeNumber, opts)}
}

// Duration registers a [time.Duration] field.
func Duration[R any, C DurationCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *DurationField {
	return &DurationField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeDuration, opts)}
}

// Time registers a [time.Time] field.
func Time[R any, C TimeCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *TimeField {
	return &TimeField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeTimestamp, opts)}
}

// Bytes registers a byte slice field.
func Bytes[R any, C BytesCarrier](s *Schema[R], field *C, name string, opts ...FieldOption) *BytesField {
	return &BytesField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeBytes, opts)}
}

// List registers a slice field.
func List[R, C any](s *Schema[R], field *C, name string, opts ...FieldOption) *ListField {
	return &ListField{registerScalar(s, unsafe.Pointer(field), reflect.TypeFor[C](), name, TypeList, opts)}
}

// Field registers a field of any supported type.
//
// It is the escape hatch for named types and for carriers the typed helpers do
// not spell, such as Optional[Port]: the semantic type and the carrier are
// resolved by reflection instead of by the helper's constraint.
func Field[R, C any](s *Schema[R], field *C, name string, opts ...FieldOption) *FieldBuilder {
	b := s.b
	reg := b.register(unsafe.Pointer(field), reflect.TypeFor[C](), name, regField)
	b.applyOptions(reg, opts)
	return &FieldBuilder{b: b, reg: reg}
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
