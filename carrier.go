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
	PresenceOptional
	// PresenceNullable is a [NullableOf] carrier: missing, null or present.
	PresenceNullable
)

// String implements [fmt.Stringer].
func (p Presence) String() string {
	switch p {
	case PresenceOptional:
		return "optional"
	case PresenceNullable:
		return "nullable"
	default:
		return "required"
	}
}

// carrierInfo is implemented by [OptionalOf] and [NullableOf]. It is unexported on
// purpose: presence is a closed set, and third-party carriers would break the
// resolution pipeline's missing/null/present model.
type carrierInfo interface {
	carrierPresence() Presence
	carrierElem() reflect.Type
}

// carrierRef is the pointer-receiver half of [carrierInfo], used to write a
// decoded value into a carrier without knowing its element type statically.
type carrierRef interface {
	carrierSet(v any) error
	carrierSetNull() error
	carrierGet() (any, bool)
}

func (OptionalOf[T]) carrierPresence() Presence { return PresenceOptional }
func (OptionalOf[T]) carrierElem() reflect.Type { return reflect.TypeFor[T]() }
func (NullableOf[T]) carrierPresence() Presence { return PresenceNullable }
func (NullableOf[T]) carrierElem() reflect.Type { return reflect.TypeFor[T]() }

func (o *OptionalOf[T]) carrierSet(v any) error {
	t, ok := v.(T)
	if !ok {
		return errors.Errorf("cannot assign %T to OptionalOf[%s]", v, reflect.TypeFor[T]())
	}
	o.Set(t)
	return nil
}

func (o *OptionalOf[T]) carrierSetNull() error {
	return errors.Errorf("OptionalOf[%s] does not accept null", reflect.TypeFor[T]())
}

func (o *OptionalOf[T]) carrierGet() (any, bool) {
	v, ok := o.Value()
	return v, ok
}

func (n *NullableOf[T]) carrierSet(v any) error {
	t, ok := v.(T)
	if !ok {
		return errors.Errorf("cannot assign %T to NullableOf[%s]", v, reflect.TypeFor[T]())
	}
	n.Set(t)
	return nil
}

func (n *NullableOf[T]) carrierSetNull() error {
	n.SetNull()
	return nil
}

func (n *NullableOf[T]) carrierGet() (any, bool) {
	v, ok := n.Value()
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
