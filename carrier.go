package figureout

import (
	"reflect"

	"github.com/go-faster/errors"
)

// Presence describes how a field models the absence of a value.
type Presence uint8

// Presence values.
const (
	// PresenceRequired is a plain Go value: it is always materialized, and a
	// missing value is an error unless a default applies.
	PresenceRequired Presence = iota
	// PresenceOptional is an [OptionalOf] carrier: missing or present.
	//
	// There is deliberately no nullable state. An explicit null in a source is
	// a merge directive that erases earlier layers, not a value a field holds,
	// so nullability never reaches the Go type.
	PresenceOptional
	// PresencePointer is a pointer carrier: nil is missing, non-nil present.
	//
	// It is [PresenceOptional] written the way encoding/json and go-faster/yaml
	// already understand. A configuration adopted onto figureout is usually
	// also marshaled, passed to library code and compared against nil by its
	// consumers, so moving such a field to [OptionalOf] ripples out of the
	// configuration package entirely; the pointer is the shape already there.
	//
	// It says nothing more than [OptionalOf] does: a nil pointer means no
	// source provided a value, exactly as an unset carrier does, and null still
	// never reaches the Go value.
	PresencePointer
)

// String implements [fmt.Stringer].
func (p Presence) String() string {
	switch p {
	case PresenceOptional:
		return "optional"
	case PresencePointer:
		return "pointer"
	default:
		return "required"
	}
}

// carrierInfo is implemented by [OptionalOf]. It is unexported on purpose:
// presence is a closed set, and a third-party carrier would break the
// resolution pipeline's missing and present model.
type carrierInfo interface {
	carrierPresence() Presence
	carrierElem() reflect.Type
}

// carrierRef is the pointer-receiver half of [carrierInfo], used to write a
// decoded value into a carrier without knowing its element type statically.
type carrierRef interface {
	carrierSet(v any) error
	carrierGet() (any, bool)
}

func (OptionalOf[T]) carrierPresence() Presence { return PresenceOptional }
func (OptionalOf[T]) carrierElem() reflect.Type { return reflect.TypeFor[T]() }

func (o *OptionalOf[T]) carrierSet(v any) error {
	t, ok := v.(T)
	if !ok {
		return errors.Errorf("cannot assign %T to OptionalOf[%s]", v, reflect.TypeFor[T]())
	}
	o.Set(t)
	return nil
}

func (o *OptionalOf[T]) carrierGet() (any, bool) {
	v, ok := o.Value()
	return v, ok
}

// unwrapCarrier reports the presence modeled by t and the Go type of the
// value it carries. A type that is not a carrier is required and carries itself.
func unwrapCarrier(t reflect.Type) (Presence, reflect.Type) {
	// A pointer is checked first: the pointer-receiver methods of [OptionalOf]
	// are in a *OptionalOf's method set, so asking a nil one what it carries
	// would call a method on it.
	if t.Kind() == reflect.Pointer {
		return PresencePointer, t.Elem()
	}
	if c, ok := reflect.New(t).Elem().Interface().(carrierInfo); ok {
		return c.carrierPresence(), c.carrierElem()
	}
	return PresenceRequired, t
}
