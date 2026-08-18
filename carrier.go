package figureout

import (
	"reflect"
	"unsafe"

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
	carrierAddr() unsafe.Pointer
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

// carrierAddr is the address of the carried value. A nested object's members
// bind by their address inside it, and the carried value is unexported, which
// reflect will address but not hand out an interface for.
func (o *OptionalOf[T]) carrierAddr() unsafe.Pointer { return unsafe.Pointer(&o.value) }

// unwrapCarrier reports the presence modeled by t, whether the value it carries
// is reached through a pointer, and the Go type of that value. A type that is
// not a carrier is required and carries itself.
//
// Presence and indirection are separate questions, and a type answers them
// separately. [OptionalOf] is the only thing that says a value may be missing;
// a pointer says only that the value is held behind one, and resolution
// allocates it. So "*C" is required and "OptionalOf[*C]" is optional, and
// neither reads the other's meaning into a nil.
func unwrapCarrier(t reflect.Type) (p Presence, indirect bool, elem reflect.Type) {
	// A pointer is checked first: the pointer-receiver methods of [OptionalOf]
	// are in a *OptionalOf's method set, so asking a nil one what it carries
	// would call a method on it.
	if t.Kind() == reflect.Pointer {
		return PresenceRequired, true, t.Elem()
	}
	if c, ok := reflect.New(t).Elem().Interface().(carrierInfo); ok {
		held := c.carrierElem()
		if held.Kind() == reflect.Pointer {
			return c.carrierPresence(), true, held.Elem()
		}
		return c.carrierPresence(), false, held
	}
	return PresenceRequired, false, t
}
