package figureout

import (
	"reflect"
	"regexp"

	"github.com/go-faster/errors"
)

// FieldBuilder is the fluent surface shared by every registered field.
//
// Methods are no-ops when the registration already failed, so a describe
// callback never panics on a bad pointer; the failure is reported as a
// compilation diagnostic instead.
type FieldBuilder struct {
	b   *builder
	reg *registration
}

func (f *FieldBuilder) ok() bool { return f != nil && f.reg != nil && f.reg.valid }

func (f *FieldBuilder) constraint(c Constraint) *FieldBuilder {
	if f.ok() {
		f.reg.constraints = append(f.reg.constraints, c)
	}
	return f
}

// Doc attaches documentation.
func (f *FieldBuilder) Doc(text string) *FieldBuilder {
	if f.ok() {
		f.reg.meta.Doc = text
	}
	return f
}

// Deprecated marks the field as deprecated.
func (f *FieldBuilder) Deprecated(reason string) *FieldBuilder {
	if f.ok() {
		f.reg.meta.Deprecated = reason
	}
	return f
}

// Hidden hides the field from generated documentation.
func (f *FieldBuilder) Hidden() *FieldBuilder {
	if f.ok() {
		f.reg.meta.Hidden = true
	}
	return f
}

// Examples attaches example values.
func (f *FieldBuilder) Examples(values ...any) *FieldBuilder {
	if f.ok() {
		f.reg.meta.Examples = append(f.reg.meta.Examples, values...)
	}
	return f
}

// With applies field options after registration.
func (f *FieldBuilder) With(opts ...FieldOption) *FieldBuilder {
	if f.ok() {
		f.b.applyOptions(f.reg, opts)
	}
	return f
}

// Name returns the canonical name of the field.
func (f *FieldBuilder) Name() string {
	if f == nil || f.reg == nil {
		return ""
	}
	return f.reg.name
}

// ValueField is the fluent builder for a value field carrying T.
//
// Constraints are typed: InRange takes two T rather than two any, so a bound
// that does not belong to the field is a compile error rather than a
// descriptor diagnostic. Constraints that do not apply to the field's semantic
// kind, such as MinLength on an integer, are still rejected during
// compilation.
type ValueField[T any] struct {
	*FieldBuilder
}

func (f *ValueField[T]) with(c Constraint) *ValueField[T] {
	f.constraint(c)
	return f
}

// Doc attaches documentation.
func (f *ValueField[T]) Doc(text string) *ValueField[T] {
	f.FieldBuilder.Doc(text)
	return f
}

// Deprecated marks the field as deprecated.
func (f *ValueField[T]) Deprecated(reason string) *ValueField[T] {
	f.FieldBuilder.Deprecated(reason)
	return f
}

// Hidden hides the field from generated documentation.
func (f *ValueField[T]) Hidden() *ValueField[T] {
	f.FieldBuilder.Hidden()
	return f
}

// Examples attaches example values.
func (f *ValueField[T]) Examples(values ...T) *ValueField[T] {
	f.FieldBuilder.Examples(anySlice(values)...)
	return f
}

// With applies field options after registration.
func (f *ValueField[T]) With(opts ...FieldOption) *ValueField[T] {
	f.FieldBuilder.With(opts...)
	return f
}

// ApplyDefault sets a default that changes the resolved value when no source
// provides one. For an [OptionalOf] field this makes the value present.
func (f *ValueField[T]) ApplyDefault(v T) *ValueField[T] {
	if f.ok() {
		f.reg.def = &Default{Value: v, Applied: true}
	}
	return f
}

// DocumentDefault records a default for documentation only, without changing
// resolution.
func (f *ValueField[T]) DocumentDefault(v T) *ValueField[T] {
	if f.ok() {
		f.reg.def = &Default{Value: v}
	}
	return f
}

// Check adds an opaque runtime validator. Opaque validators never contribute
// to generated schemas.
func (f *ValueField[T]) Check(name string, fn func(T) error) *ValueField[T] {
	return f.with(CheckConstraint{Name: name, Func: typedCheck(name, fn)})
}

// Enum restricts the field to a set of values.
//
// Prefer [Enum] or [EnumSlice] when the type enumerates its own values, so
// that the value set has a single source of truth.
func (f *ValueField[T]) Enum(values ...T) *ValueField[T] {
	if len(values) == 0 {
		if f.ok() {
			f.b.diags.errorf(CodeConstraintMismatch, f.reg.goName, f.reg.name, "enum has no values")
		}
		return f
	}
	return f.with(EnumConstraint{Values: anySlice(values)})
}

// InRange bounds the value inclusively.
func (f *ValueField[T]) InRange(minimum, maximum T) *ValueField[T] {
	return f.with(RangeConstraint{Minimum: minimum, Maximum: maximum})
}

// AtLeast sets an inclusive lower bound.
func (f *ValueField[T]) AtLeast(minimum T) *ValueField[T] {
	return f.with(RangeConstraint{Minimum: minimum})
}

// AtMost sets an inclusive upper bound.
func (f *ValueField[T]) AtMost(maximum T) *ValueField[T] {
	return f.with(RangeConstraint{Maximum: maximum})
}

// GreaterThan sets an exclusive lower bound.
func (f *ValueField[T]) GreaterThan(minimum T) *ValueField[T] {
	return f.with(RangeConstraint{Minimum: minimum, ExclusiveMinimum: true})
}

// LessThan sets an exclusive upper bound.
func (f *ValueField[T]) LessThan(maximum T) *ValueField[T] {
	return f.with(RangeConstraint{Maximum: maximum, ExclusiveMaximum: true})
}

// MergeReplace takes the value from the last layer that provided one. It is
// the default.
func (f *ValueField[T]) MergeReplace() *ValueField[T] { return f.mergeWith(MergeReplace) }

// MergeAppend concatenates list values across layers, in layer order.
func (f *ValueField[T]) MergeAppend() *ValueField[T] { return f.mergeWith(MergeAppend) }

// MergeByKey merges map entries across layers, so a later layer changes only
// the keys it names.
func (f *ValueField[T]) MergeByKey() *ValueField[T] { return f.mergeWith(MergeByKey) }

func (f *ValueField[T]) mergeWith(p MergePolicy) *ValueField[T] {
	if f.ok() {
		f.reg.merge = p
	}
	return f
}

// NonEmpty requires a length of at least one.
func (f *ValueField[T]) NonEmpty() *ValueField[T] {
	return f.MinLength(1)
}

// MinLength requires at least n characters, bytes or elements.
func (f *ValueField[T]) MinLength(n uint64) *ValueField[T] {
	return f.with(LengthConstraint{Minimum: &n})
}

// MaxLength allows at most n characters, bytes or elements.
func (f *ValueField[T]) MaxLength(n uint64) *ValueField[T] {
	return f.with(LengthConstraint{Maximum: &n})
}

// MinItems requires at least n elements.
func (f *ValueField[T]) MinItems(n uint64) *ValueField[T] { return f.MinLength(n) }

// MaxItems allows at most n elements.
func (f *ValueField[T]) MaxItems(n uint64) *ValueField[T] { return f.MaxLength(n) }

// Pattern requires the value to match an RE2 regular expression.
func (f *ValueField[T]) Pattern(expr string) *ValueField[T] {
	re, err := regexp.Compile(expr)
	if err != nil {
		if f.ok() {
			f.b.diags.errorf(CodeConstraintMismatch, f.reg.goName, f.reg.name,
				"invalid pattern %q: %s", expr, err)
		}
		return f
	}
	return f.with(PatternConstraint{Expression: expr, Dialect: PatternRE2, re: re})
}

// ObjectField is a fluent builder for nested object fields.
type ObjectField struct{ *FieldBuilder }

// Doc attaches documentation.
func (f *ObjectField) Doc(text string) *ObjectField {
	f.FieldBuilder.Doc(text)
	return f
}

// UnionField is a fluent builder for union fields.
type UnionField struct{ *FieldBuilder }

// Doc attaches documentation.
func (f *UnionField) Doc(text string) *UnionField {
	f.FieldBuilder.Doc(text)
	return f
}

// Check builds an opaque runtime validator option with a typed callback.
//
// The type argument is inferred from fn, so a call site reads as
// figureout.Check("even", func(v int) error { ... }).
func Check[T any](name string, fn func(T) error) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddConstraint(CheckConstraint{Name: name, Func: typedCheck(name, fn)})
	})
}

func typedCheck[T any](name string, fn func(T) error) func(any) error {
	return func(v any) error {
		t, ok := v.(T)
		if !ok {
			return errors.Errorf("validator %q expects %s, got %T", name, reflect.TypeFor[T](), v)
		}
		return fn(t)
	}
}
