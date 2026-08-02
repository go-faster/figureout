package figureout

import "fmt"

type nullableState uint8

const (
	nullableMissing nullableState = iota
	nullableNull
	nullablePresent
)

// NullableOf represents a value that is missing, explicitly null, or present.
//
// Only sources that model null, such as JSON and YAML, may produce the null
// state; sources without it, such as environment variables, may not.
type NullableOf[T any] struct {
	value T
	state nullableState
}

// Null returns an explicitly null [NullableOf].
func Null[T any]() NullableOf[T] {
	return NullableOf[T]{state: nullableNull}
}

// Present returns a present [NullableOf].
func Present[T any](v T) NullableOf[T] {
	return NullableOf[T]{value: v, state: nullablePresent}
}

// IsSet reports whether the value is present, that is, neither missing nor null.
func (n NullableOf[T]) IsSet() bool { return n.state == nullablePresent }

// IsNull reports whether the value is explicitly null.
func (n NullableOf[T]) IsNull() bool { return n.state == nullableNull }

// IsMissing reports whether no source provided the value.
func (n NullableOf[T]) IsMissing() bool { return n.state == nullableMissing }

// Value returns the value and whether it is present.
func (n NullableOf[T]) Value() (T, bool) { return n.value, n.state == nullablePresent }

// OrElse returns the value if present, otherwise v.
func (n NullableOf[T]) OrElse(v T) T {
	if n.state == nullablePresent {
		return n.value
	}
	return v
}

// Set makes the value present.
func (n *NullableOf[T]) Set(v T) {
	n.value, n.state = v, nullablePresent
}

// SetNull makes the value explicitly null.
func (n *NullableOf[T]) SetNull() {
	var zero T
	n.value, n.state = zero, nullableNull
}

// Clear makes the value missing.
func (n *NullableOf[T]) Clear() {
	var zero T
	n.value, n.state = zero, nullableMissing
}

// String implements [fmt.Stringer].
func (n NullableOf[T]) String() string {
	switch n.state {
	case nullableNull:
		return "null"
	case nullablePresent:
		return fmt.Sprintf("present(%v)", n.value)
	default:
		return "missing"
	}
}
