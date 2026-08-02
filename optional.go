package figureout

import "fmt"

// OptionalOf represents a value that is either missing or present.
//
// Unlike a pointer, it carries no aliasing and distinguishes "not provided by
// any source" from "provided as the zero value". Use [NullableOf] when a source
// may also provide an explicit null.
type OptionalOf[T any] struct {
	value T
	set   bool
}

// Some returns a present [OptionalOf].
func Some[T any](v T) OptionalOf[T] {
	return OptionalOf[T]{value: v, set: true}
}

// None returns a missing [OptionalOf].
func None[T any]() OptionalOf[T] {
	return OptionalOf[T]{}
}

// IsSet reports whether a value is present.
func (o OptionalOf[T]) IsSet() bool { return o.set }

// Value returns the value and whether it is present.
func (o OptionalOf[T]) Value() (T, bool) { return o.value, o.set }

// OrElse returns the value if present, otherwise v.
func (o OptionalOf[T]) OrElse(v T) T {
	if o.set {
		return o.value
	}
	return v
}

// Set makes the value present.
func (o *OptionalOf[T]) Set(v T) {
	o.value, o.set = v, true
}

// Clear makes the value missing and resets it to the zero value.
func (o *OptionalOf[T]) Clear() {
	var zero T
	o.value, o.set = zero, false
}

// String implements [fmt.Stringer].
func (o OptionalOf[T]) String() string {
	if !o.set {
		return "none"
	}
	return fmt.Sprintf("some(%v)", o.value)
}
