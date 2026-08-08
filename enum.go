package figureout

import (
	"iter"
	"reflect"
	"slices"
	"unsafe"
)

// EnumValuer is a type that enumerates its own values.
//
// It is the primary enum contract: because it is a constraint rather than a
// reflective probe, a type without values is a compile error rather than a
// descriptor diagnostic.
//
//	func (LogLevel) AllValues() iter.Seq[LogLevel] {
//		return slices.Values(logLevels)
//	}
type EnumValuer[T any] interface {
	AllValues() iter.Seq[T]
}

// EnumSliceValuer is the slice-returning form of [EnumValuer], as emitted by
// stringer derivatives that attach a method to the type.
type EnumSliceValuer[T any] interface {
	Values() []T
}

// Enum registers a field whose type enumerates its own values.
//
// An enum is a set of allowed values for one type. It is distinct from [OneOf],
// which selects between alternative shapes.
//
// An enum field is required, as [Explicit] is: the zero value of an enumerated
// type is rarely one of its members, so absence needs a default rather than a
// fallback nobody declared.
func Enum[R any, T EnumValuer[T]](s *Schema[R], field *T, name string, opts ...FieldOption) *ValueField[T] {
	var zero T
	return registerEnum(s, unsafe.Pointer(field), reflect.TypeFor[T](), name,
		slices.Collect(zero.AllValues()), opts)
}

// EnumSlice registers a field whose type enumerates its own values through a
// Values method.
func EnumSlice[R any, T EnumSliceValuer[T]](s *Schema[R], field *T, name string, opts ...FieldOption) *ValueField[T] {
	var zero T
	return registerEnum(s, unsafe.Pointer(field), reflect.TypeFor[T](), name, zero.Values(), opts)
}

// EnumFunc registers an enumerated field whose values come from a function.
//
// Generators that emit a package-level function rather than a method, such as
// enumer's LogLevelValues, are registered this way.
func EnumFunc[R, T any](s *Schema[R], field *T, name string, values func() []T, opts ...FieldOption) *ValueField[T] {
	var vs []T
	if values != nil {
		vs = values()
	}
	return registerEnum(s, unsafe.Pointer(field), reflect.TypeFor[T](), name, vs, opts)
}

// EnumValues registers an enumerated field with an explicit set of values.
func EnumValues[R, T any](s *Schema[R], field *T, name string, values []T, opts ...FieldOption) *ValueField[T] {
	return registerEnum(s, unsafe.Pointer(field), reflect.TypeFor[T](), name, values, opts)
}

func registerEnum[R, T any](
	s *Schema[R],
	ptr unsafe.Pointer,
	carrier reflect.Type,
	name string,
	values []T,
	opts []FieldOption,
) *ValueField[T] {
	b := s.b
	reg := b.register(ptr, carrier, name, regField)
	if reg.valid {
		if len(values) == 0 {
			b.diags.errorf(CodeConstraintMismatch, reg.goName, name, "enum has no values")
		} else {
			reg.constraints = append(reg.constraints, EnumConstraint{Values: anySlice(values)})
		}
	}
	b.applyOptions(reg, opts)
	return &ValueField[T]{&FieldBuilder{b: b, reg: reg}}
}

// EnumOf builds an enum constraint option for a type that enumerates its own
// values. Use it with [Field] for carriers the enum helpers do not spell, such
// as OptionalOf[LogLevel].
func EnumOf[T EnumValuer[T]]() FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		var zero T
		return c.AddConstraint(EnumConstraint{Values: anySlice(slices.Collect(zero.AllValues()))})
	})
}

// EnumOfSlice is [EnumOf] for types implementing [EnumSliceValuer].
func EnumOfSlice[T EnumSliceValuer[T]]() FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		var zero T
		return c.AddConstraint(EnumConstraint{Values: anySlice(zero.Values())})
	})
}

// EnumOfValues builds an enum constraint option from explicit values.
func EnumOfValues[T any](values ...T) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddConstraint(EnumConstraint{Values: anySlice(values)})
	})
}

// EnumOfFunc builds an enum constraint option from a values function.
func EnumOfFunc[T any](values func() []T) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		var vs []T
		if values != nil {
			vs = values()
		}
		return c.AddConstraint(EnumConstraint{Values: anySlice(vs)})
	})
}

func anySlice[T any](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// EnumValuesOf returns the allowed values declared for a field, if any.
func EnumValuesOf(f *FieldModel) ([]any, bool) {
	for _, c := range f.Constraints {
		if e, ok := c.(EnumConstraint); ok {
			return e.Values, true
		}
	}
	return nil, false
}
