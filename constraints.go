package figureout

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/go-faster/errors"
)

// Constraint is a declarative rule over a semantic value.
//
// Constraints are values rather than closures so that validators, schema
// emitters and documentation can all consume the same declaration. Rules that
// cannot be expressed declaratively use [CheckConstraint], which is
// runtime-only.
type Constraint interface {
	// Kind identifies the constraint for emitters and diagnostics.
	Kind() string
	// Validate reports whether v satisfies the constraint.
	Validate(v any) error
	// Applies reports whether the constraint is meaningful for kind.
	Applies(kind TypeKind) bool
}

// RangeConstraint bounds a numeric or duration value.
type RangeConstraint struct {
	Minimum          any
	Maximum          any
	ExclusiveMinimum bool
	ExclusiveMaximum bool
}

// Kind implements [Constraint].
func (RangeConstraint) Kind() string { return "range" }

// Applies implements [Constraint].
func (RangeConstraint) Applies(kind TypeKind) bool {
	switch kind {
	case TypeInteger, TypeNumber, TypeDuration, TypeTimestamp:
		return true
	default:
		return false
	}
}

// Validate implements [Constraint].
func (c RangeConstraint) Validate(v any) error {
	if c.Minimum != nil {
		cmp, err := compareValues(v, c.Minimum)
		if err != nil {
			return err
		}
		if cmp < 0 || (cmp == 0 && c.ExclusiveMinimum) {
			return errors.Errorf("must be %s %v", gteOp(c.ExclusiveMinimum), c.Minimum)
		}
	}
	if c.Maximum != nil {
		cmp, err := compareValues(v, c.Maximum)
		if err != nil {
			return err
		}
		if cmp > 0 || (cmp == 0 && c.ExclusiveMaximum) {
			return errors.Errorf("must be %s %v", lteOp(c.ExclusiveMaximum), c.Maximum)
		}
	}
	return nil
}

func gteOp(exclusive bool) string {
	if exclusive {
		return "greater than"
	}
	return "at least"
}

func lteOp(exclusive bool) string {
	if exclusive {
		return "less than"
	}
	return "at most"
}

// LengthConstraint bounds the length of a string, list, map or byte slice.
type LengthConstraint struct {
	Minimum *uint64
	Maximum *uint64
}

// Kind implements [Constraint].
func (LengthConstraint) Kind() string { return "length" }

// Applies implements [Constraint].
func (LengthConstraint) Applies(kind TypeKind) bool {
	switch kind {
	case TypeString, TypeBytes, TypeList, TypeMap:
		return true
	default:
		return false
	}
}

// Validate implements [Constraint].
func (c LengthConstraint) Validate(v any) error {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
	default:
		return errors.Errorf("length is not defined for %T", v)
	}

	n := uint64(rv.Len())
	if c.Minimum != nil && n < *c.Minimum {
		return errors.Errorf("length must be at least %d, got %d", *c.Minimum, n)
	}
	if c.Maximum != nil && n > *c.Maximum {
		return errors.Errorf("length must be at most %d, got %d", *c.Maximum, n)
	}
	return nil
}

// EnumConstraint restricts a value to a set of allowed values.
//
// It is deliberately distinct from a union: an enum narrows one scalar type,
// while a [Union] selects between alternative shapes.
type EnumConstraint struct {
	Values []any
}

// Kind implements [Constraint].
func (EnumConstraint) Kind() string { return "enum" }

// Applies implements [Constraint].
func (EnumConstraint) Applies(kind TypeKind) bool {
	switch kind {
	case TypeObject, TypeUnion, TypeList, TypeMap:
		return false
	default:
		return true
	}
}

// Validate implements [Constraint].
func (c EnumConstraint) Validate(v any) error {
	for _, allowed := range c.Values {
		if reflect.DeepEqual(v, allowed) {
			return nil
		}
	}

	labels := make([]string, len(c.Values))
	for i, allowed := range c.Values {
		labels[i] = fmt.Sprintf("%v", allowed)
	}
	return errors.Errorf("must be one of [%s], got %v", strings.Join(labels, ", "), v)
}

// PatternDialect names the regular expression syntax of a [PatternConstraint].
type PatternDialect uint8

// Pattern dialects.
const (
	// PatternRE2 is Go's [regexp] syntax.
	PatternRE2 PatternDialect = iota
	// PatternECMA is the syntax used by JSON Schema.
	PatternECMA
)

// PatternConstraint restricts a string to a regular expression.
type PatternConstraint struct {
	Expression string
	Dialect    PatternDialect

	re *regexp.Regexp
}

// Kind implements [Constraint].
func (PatternConstraint) Kind() string { return "pattern" }

// Applies implements [Constraint].
func (PatternConstraint) Applies(kind TypeKind) bool { return kind == TypeString }

// Validate implements [Constraint].
func (c PatternConstraint) Validate(v any) error {
	s, ok := v.(string)
	if !ok {
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.String {
			return errors.Errorf("pattern is not defined for %T", v)
		}
		s = rv.String()
	}
	if c.re == nil {
		return errors.New("pattern was not compiled")
	}
	if !c.re.MatchString(s) {
		return errors.Errorf("must match %q", c.Expression)
	}
	return nil
}

// CheckConstraint is an opaque runtime validator.
//
// It never contributes to generated schemas; emitters report
// [CodeValidatorNotExport] instead of silently implying coverage.
type CheckConstraint struct {
	Name string
	Func func(v any) error
}

// Kind implements [Constraint].
func (CheckConstraint) Kind() string { return "check" }

// Applies implements [Constraint].
func (CheckConstraint) Applies(TypeKind) bool { return true }

// Validate implements [Constraint].
func (c CheckConstraint) Validate(v any) error {
	if c.Func == nil {
		return nil
	}
	if err := c.Func(v); err != nil {
		return errors.Wrap(err, c.Name)
	}
	return nil
}

// compareValues orders two numeric, duration or string values.
func compareValues(a, b any) (int, error) {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	switch {
	case isSigned(av) && isSigned(bv):
		return cmpOrdered(av.Int(), bv.Int()), nil
	case isUnsigned(av) && isUnsigned(bv):
		return cmpOrdered(av.Uint(), bv.Uint()), nil
	case isSigned(av) && isUnsigned(bv):
		if bv.Uint() > 1<<63-1 {
			return -1, nil
		}
		return cmpOrdered(av.Int(), int64(bv.Uint())), nil
	case isUnsigned(av) && isSigned(bv):
		if av.Uint() > 1<<63-1 {
			return 1, nil
		}
		return cmpOrdered(int64(av.Uint()), bv.Int()), nil
	case isFloat(av) || isFloat(bv):
		af, err := toFloat(av)
		if err != nil {
			return 0, err
		}
		bf, err := toFloat(bv)
		if err != nil {
			return 0, err
		}
		return cmpOrdered(af, bf), nil
	case av.Kind() == reflect.String && bv.Kind() == reflect.String:
		return strings.Compare(av.String(), bv.String()), nil
	}
	return 0, errors.Errorf("cannot compare %T with %T", a, b)
}

func cmpOrdered[T int64 | uint64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func isSigned(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	default:
		return false
	}
}

func isUnsigned(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

func isFloat(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func toFloat(v reflect.Value) (float64, error) {
	switch {
	case isFloat(v):
		return v.Float(), nil
	case isSigned(v):
		return float64(v.Int()), nil
	case isUnsigned(v):
		return float64(v.Uint()), nil
	default:
		return 0, errors.Errorf("not a number: %s", v.Kind())
	}
}
