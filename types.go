package figureout

import (
	"reflect"
	"time"
)

// TypeKind is the format-neutral meaning of a value.
type TypeKind uint8

// Semantic kinds.
const (
	TypeInvalid TypeKind = iota
	TypeBoolean
	TypeInteger
	TypeNumber
	TypeString
	TypeBytes
	TypeDuration
	TypeTimestamp
	TypeList
	TypeMap
	TypeObject
	TypeUnion
)

var typeKindNames = [...]string{
	TypeInvalid:   "invalid",
	TypeBoolean:   "boolean",
	TypeInteger:   "integer",
	TypeNumber:    "number",
	TypeString:    "string",
	TypeBytes:     "bytes",
	TypeDuration:  "duration",
	TypeTimestamp: "timestamp",
	TypeList:      "list",
	TypeMap:       "map",
	TypeObject:    "object",
	TypeUnion:     "union",
}

// String implements [fmt.Stringer].
func (k TypeKind) String() string {
	if int(k) >= len(typeKindNames) {
		return "invalid"
	}
	return typeKindNames[k]
}

// Type is a semantic type. It never refers to a wire format; see
// [SourceProjection] for the representation accepted by a given source.
type Type struct {
	Kind TypeKind

	// Go is the Go type carrying the value, with [OptionalOf] already
	// unwrapped.
	Go reflect.Type

	// Unit scales a bare number written for a [TypeDuration] field, so that
	// "timeout_seconds: 180" resolves to 180 * time.Second. Zero means the
	// field is only spelled as a duration. See [Unit].
	Unit time.Duration

	Elem   *Type        // list element, map value
	Key    *Type        // map key
	Object *ObjectModel // object fields
	Union  *Union       // union variants

	// Scalar is the scalar spelling an object also accepts, as set by
	// [ScalarOr]. It is nil for an object that is only ever written as one.
	Scalar *Type
}

// UnitName names what a unit-scaled integer counts, for diagnostics and
// generated documentation. It is empty when the type declares no unit.
func (t Type) UnitName() string {
	switch t.Unit {
	case 0:
		return ""
	case time.Nanosecond:
		return "nanoseconds"
	case time.Microsecond:
		return "microseconds"
	case time.Millisecond:
		return "milliseconds"
	case time.Second:
		return "seconds"
	case time.Minute:
		return "minutes"
	case time.Hour:
		return "hours"
	default:
		return "units of " + t.Unit.String()
	}
}

// Union is a tagged sum of object variants.
//
// A union is distinct from an enumeration: an enum constrains a scalar to a
// set of values, while a union selects between alternative shapes.
type Union struct {
	// Discriminator is the property carrying the variant tag.
	Discriminator string
	Variants      []*VariantModel
}

// VariantModel is one alternative of a [Union].
type VariantModel struct {
	// Tag is the discriminator value selecting this variant.
	Tag string
	// GoPath locates the variant field inside the union container.
	GoPath FieldPath
	// Object describes the variant payload.
	Object *ObjectModel

	acc accessor
}

var (
	durationType = reflect.TypeFor[time.Duration]()
	timeType     = reflect.TypeFor[time.Time]()
	bytesType    = reflect.TypeFor[[]byte]()
)

// deriveType maps a Go type to its default semantic type. Named types may be
// refined further by a [TypeRegistry] or by explicit registration.
func deriveType(t reflect.Type) (Type, bool) {
	switch t {
	case durationType:
		return Type{Kind: TypeDuration, Go: t}, true
	case timeType:
		return Type{Kind: TypeTimestamp, Go: t}, true
	case bytesType:
		return Type{Kind: TypeBytes, Go: t}, true
	}

	switch t.Kind() {
	case reflect.Bool:
		return Type{Kind: TypeBoolean, Go: t}, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Type{Kind: TypeInteger, Go: t}, true
	case reflect.Float32, reflect.Float64:
		return Type{Kind: TypeNumber, Go: t}, true
	case reflect.String:
		return Type{Kind: TypeString, Go: t}, true
	case reflect.Slice, reflect.Array:
		elem, ok := deriveType(t.Elem())
		if !ok {
			return Type{}, false
		}
		return Type{Kind: TypeList, Go: t, Elem: &elem}, true
	case reflect.Map:
		key, ok := deriveType(t.Key())
		if !ok {
			return Type{}, false
		}
		elem, ok := deriveType(t.Elem())
		if !ok {
			return Type{}, false
		}
		return Type{Kind: TypeMap, Go: t, Key: &key, Elem: &elem}, true
	case reflect.Struct:
		return Type{Kind: TypeObject, Go: t}, true
	default:
		return Type{}, false
	}
}
