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
	// There is deliberately no third state. An explicit null in a source is a
	// merge directive that erases earlier layers, not a value a field holds,
	// so nullability never reaches the Go type.
	PresenceOptional
)

// String implements [fmt.Stringer].
func (p Presence) String() string {
	if p == PresenceOptional {
		return "optional"
	}
	return "required"
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

// unwrapCarrier reports the presence modelled by t and the Go type of the
// value it carries. A type that is not a carrier is required and carries itself.
func unwrapCarrier(t reflect.Type) (Presence, reflect.Type) {
	if c, ok := reflect.New(t).Elem().Interface().(carrierInfo); ok {
		return c.carrierPresence(), c.carrierElem()
	}
	return PresenceRequired, t
}
