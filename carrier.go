package figureout

import (
	"reflect"
	"time"

	"github.com/go-faster/errors"
)

// Presence describes how a field models the absence of a value.
type Presence uint8

// Presence values.
const (
	// PresenceRequired is a plain Go value: it is always materialized, and a
	// missing value is an error unless a default applies.
	PresenceRequired Presence = iota
	// PresenceOptional is an [Optional] carrier: missing or present.
	PresenceOptional
	// PresenceNullable is a [Nullable] carrier: missing, null or present.
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

// carrierInfo is implemented by [Optional] and [Nullable]. It is unexported on
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

func (Optional[T]) carrierPresence() Presence { return PresenceOptional }
func (Optional[T]) carrierElem() reflect.Type { return reflect.TypeFor[T]() }
func (Nullable[T]) carrierPresence() Presence { return PresenceNullable }
func (Nullable[T]) carrierElem() reflect.Type { return reflect.TypeFor[T]() }

func (o *Optional[T]) carrierSet(v any) error {
	t, ok := v.(T)
	if !ok {
		return errors.Errorf("cannot assign %T to Optional[%s]", v, reflect.TypeFor[T]())
	}
	o.Set(t)
	return nil
}

func (o *Optional[T]) carrierSetNull() error {
	return errors.Errorf("Optional[%s] does not accept null", reflect.TypeFor[T]())
}

func (o *Optional[T]) carrierGet() (any, bool) {
	v, ok := o.Value()
	return v, ok
}

func (n *Nullable[T]) carrierSet(v any) error {
	t, ok := v.(T)
	if !ok {
		return errors.Errorf("cannot assign %T to Nullable[%s]", v, reflect.TypeFor[T]())
	}
	n.Set(t)
	return nil
}

func (n *Nullable[T]) carrierSetNull() error {
	n.SetNull()
	return nil
}

func (n *Nullable[T]) carrierGet() (any, bool) {
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

// Carrier constraints.
//
// Go forbids a bare type parameter as a union term, so a single generic
// Carrier[T] covering both T and Optional[T] cannot be spelled. Each semantic
// helper therefore names a concrete carrier constraint, which keeps one helper
// per semantic type with full type inference at the call site.
//
// Carriers of named types beyond these, such as Optional[Port], are registered
// through [Field], which resolves the carrier by reflection.
type (
	// BoolCarrier carries a boolean.
	BoolCarrier interface {
		~bool | Optional[bool] | Nullable[bool]
	}
	// StringCarrier carries a string.
	StringCarrier interface {
		~string | Optional[string] | Nullable[string]
	}
	// IntCarrier carries an integer.
	IntCarrier interface {
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
			~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
			Optional[int] | Nullable[int] |
			Optional[int64] | Nullable[int64]
	}
	// FloatCarrier carries a floating point number.
	FloatCarrier interface {
		~float32 | ~float64 | Optional[float64] | Nullable[float64]
	}
	// DurationCarrier carries a [time.Duration].
	DurationCarrier interface {
		time.Duration | Optional[time.Duration] | Nullable[time.Duration]
	}
	// TimeCarrier carries a [time.Time].
	TimeCarrier interface {
		time.Time | Optional[time.Time] | Nullable[time.Time]
	}
	// BytesCarrier carries a byte slice.
	BytesCarrier interface {
		~[]byte | Optional[[]byte] | Nullable[[]byte]
	}
)
