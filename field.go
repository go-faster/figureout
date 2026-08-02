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

func (f *FieldBuilder) ok() bool { return f != nil && f.reg != nil && f.reg.goName != "" }

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

// ApplyDefault sets a default that changes the resolved value when no source
// provides one. For an [Optional] field this makes the value present.
func (f *FieldBuilder) ApplyDefault(v any) *FieldBuilder {
	if f.ok() {
		f.reg.def = &Default{Value: v, Applied: true}
	}
	return f
}

// DocumentDefault records a default for documentation only, without changing
// resolution.
func (f *FieldBuilder) DocumentDefault(v any) *FieldBuilder {
	if f.ok() {
		f.reg.def = &Default{Value: v}
	}
	return f
}

// Check adds an opaque runtime validator. Opaque validators never contribute
// to generated schemas.
func (f *FieldBuilder) Check(name string, fn func(v any) error) *FieldBuilder {
	return f.constraint(CheckConstraint{Name: name, Func: fn})
}

// With applies field options after registration.
func (f *FieldBuilder) With(opts ...FieldOption) *FieldBuilder {
	if f.ok() {
		f.b.applyOptions(f.reg, opts)
	}
	return f
}

// Field returns the registration name of the field.
func (f *FieldBuilder) Field() string {
	if f == nil || f.reg == nil {
		return ""
	}
	return f.reg.name
}

// StringField is a fluent builder for string fields.
type StringField struct{ *FieldBuilder }

// NonEmpty requires a length of at least one.
func (f *StringField) NonEmpty() *StringField {
	return f.MinLength(1)
}

// MinLength requires at least n characters.
func (f *StringField) MinLength(n uint64) *StringField {
	f.constraint(LengthConstraint{Minimum: &n})
	return f
}

// MaxLength allows at most n characters.
func (f *StringField) MaxLength(n uint64) *StringField {
	f.constraint(LengthConstraint{Maximum: &n})
	return f
}

// Pattern requires the value to match an RE2 regular expression.
func (f *StringField) Pattern(expr string) *StringField {
	re, err := regexp.Compile(expr)
	if err != nil {
		if f.ok() {
			f.b.diags.errorf(CodeConstraintMismatch, f.reg.goName, f.reg.name,
				"invalid pattern %q: %s", expr, err)
		}
		return f
	}
	f.constraint(PatternConstraint{Expression: expr, Dialect: PatternRE2, re: re})
	return f
}

// RangeField is a fluent builder for ordered scalar fields.
type RangeField struct{ *FieldBuilder }

// Aliases for the semantic helpers returning an ordered scalar.
type (
	// IntField is returned by [Int].
	IntField = RangeField
	// FloatField is returned by [Float].
	FloatField = RangeField
	// DurationField is returned by [Duration].
	DurationField = RangeField
	// TimeField is returned by [Time].
	TimeField = RangeField
)

// InRange bounds the value inclusively.
func (f *RangeField) InRange(minimum, maximum any) *RangeField {
	f.constraint(RangeConstraint{Minimum: minimum, Maximum: maximum})
	return f
}

// AtLeast sets an inclusive lower bound.
func (f *RangeField) AtLeast(minimum any) *RangeField {
	f.constraint(RangeConstraint{Minimum: minimum})
	return f
}

// AtMost sets an inclusive upper bound.
func (f *RangeField) AtMost(maximum any) *RangeField {
	f.constraint(RangeConstraint{Maximum: maximum})
	return f
}

// GreaterThan sets an exclusive lower bound.
func (f *RangeField) GreaterThan(minimum any) *RangeField {
	f.constraint(RangeConstraint{Minimum: minimum, ExclusiveMinimum: true})
	return f
}

// LessThan sets an exclusive upper bound.
func (f *RangeField) LessThan(maximum any) *RangeField {
	f.constraint(RangeConstraint{Maximum: maximum, ExclusiveMaximum: true})
	return f
}

// BoolField is a fluent builder for boolean fields.
type BoolField struct{ *FieldBuilder }

// BytesField is a fluent builder for byte slice fields.
type BytesField struct{ *FieldBuilder }

// MinLength requires at least n bytes.
func (f *BytesField) MinLength(n uint64) *BytesField {
	f.constraint(LengthConstraint{Minimum: &n})
	return f
}

// MaxLength allows at most n bytes.
func (f *BytesField) MaxLength(n uint64) *BytesField {
	f.constraint(LengthConstraint{Maximum: &n})
	return f
}

// ListField is a fluent builder for list fields.
type ListField struct{ *FieldBuilder }

// MinItems requires at least n elements.
func (f *ListField) MinItems(n uint64) *ListField {
	f.constraint(LengthConstraint{Minimum: &n})
	return f
}

// MaxItems allows at most n elements.
func (f *ListField) MaxItems(n uint64) *ListField {
	f.constraint(LengthConstraint{Maximum: &n})
	return f
}

// ObjectField is a fluent builder for nested object fields.
type ObjectField struct{ *FieldBuilder }

// Check adds a validator over the whole nested value.
func (f *ObjectField) Check(name string, fn func(v any) error) *ObjectField {
	f.FieldBuilder.Check(name, fn)
	return f
}

// Check option constructors.

// Check builds an opaque runtime validator option with a typed callback.
//
// The type argument is inferred from fn, so a call site reads as
// figureout.Check("even", func(v int) error { ... }).
func Check[T any](name string, fn func(T) error) FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddConstraint(CheckConstraint{
			Name: name,
			Func: func(v any) error {
				t, ok := v.(T)
				if !ok {
					return errors.Errorf("validator %q expects %s, got %T", name, reflect.TypeFor[T](), v)
				}
				return fn(t)
			},
		})
	})
}
