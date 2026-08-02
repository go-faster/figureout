package figureout

import "fmt"

type nullableState uint8

const (
	nullableMissing nullableState = iota
	nullableNull
	nullablePresent
)

// Nullable represents a value that is missing, explicitly null, or present.
//
// Only sources that model null, such as JSON and YAML, may produce the null
// state; sources without it, such as environment variables, may not.
type Nullable[T any] struct {
	value T
	state nullableState
}

// Null returns an explicitly null [Nullable].
func Null[T any]() Nullable[T] {
	return Nullable[T]{state: nullableNull}
}

// Present returns a present [Nullable].
func Present[T any](v T) Nullable[T] {
	return Nullable[T]{value: v, state: nullablePresent}
}

// IsSet reports whether the value is present, that is, neither missing nor null.
func (n Nullable[T]) IsSet() bool { return n.state == nullablePresent }

// IsNull reports whether the value is explicitly null.
func (n Nullable[T]) IsNull() bool { return n.state == nullableNull }

// IsMissing reports whether no source provided the value.
func (n Nullable[T]) IsMissing() bool { return n.state == nullableMissing }

// Value returns the value and whether it is present.
func (n Nullable[T]) Value() (T, bool) { return n.value, n.state == nullablePresent }

// OrElse returns the value if present, otherwise v.
func (n Nullable[T]) OrElse(v T) T {
	if n.state == nullablePresent {
		return n.value
	}
	return v
}

// Set makes the value present.
func (n *Nullable[T]) Set(v T) {
	n.value, n.state = v, nullablePresent
}

// SetNull makes the value explicitly null.
func (n *Nullable[T]) SetNull() {
	var zero T
	n.value, n.state = zero, nullableNull
}

// Clear makes the value missing.
func (n *Nullable[T]) Clear() {
	var zero T
	n.value, n.state = zero, nullableMissing
}

// String implements [fmt.Stringer].
func (n Nullable[T]) String() string {
	switch n.state {
	case nullableNull:
		return "null"
	case nullablePresent:
		return fmt.Sprintf("present(%v)", n.value)
	default:
		return "missing"
	}
}
