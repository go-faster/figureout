package figureout

import (
	"reflect"

	"github.com/go-faster/errors"
)

// TypeRegistry describes named domain types once, so that every field of that
// type inherits the same semantics, constraints and source options.
//
// Registries are explicit: there is no global registration. A registry must not
// be modified after it has been passed to [Derive].
type TypeRegistry struct {
	types map[reflect.Type]*typeEntry
}

type typeEntry struct {
	typ         Type
	kindSet     bool
	constraints []Constraint
	options     []FieldOption
}

// NewTypeRegistry returns an empty registry.
func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{types: map[reflect.Type]*typeEntry{}}
}

// TypeOption describes a registered type.
type TypeOption interface {
	applyType(*typeEntry) error
}

type typeOptionFunc func(*typeEntry) error

func (f typeOptionFunc) applyType(e *typeEntry) error { return f(e) }

// RegisterType describes the named type T.
func RegisterType[T any](r *TypeRegistry, opts ...TypeOption) error {
	if r == nil {
		return errors.New("nil registry")
	}
	t := reflect.TypeFor[T]()
	e := &typeEntry{}
	if derived, ok := deriveType(t); ok {
		e.typ = derived
	}
	for _, o := range opts {
		if err := o.applyType(e); err != nil {
			return errors.Wrapf(err, "type %s", t)
		}
	}
	e.typ.Go = t
	if e.typ.Kind == TypeInvalid {
		return errors.Errorf("type %s: no semantic kind", t)
	}
	r.types[t] = e
	return nil
}

// MustRegisterType is like [RegisterType] but panics on error.
func MustRegisterType[T any](r *TypeRegistry, opts ...TypeOption) {
	if err := RegisterType[T](r, opts...); err != nil {
		panic("figureout: " + err.Error())
	}
}

func (r *TypeRegistry) lookup(t reflect.Type) (Type, bool) {
	e, ok := r.types[t]
	if !ok {
		return Type{}, false
	}
	return e.typ, true
}

// apply attaches registered constraints and options to a registration.
func (r *TypeRegistry) apply(reg *registration) {
	e, ok := r.types[reg.acc.elem]
	if !ok {
		return
	}
	reg.constraints = append(reg.constraints, e.constraints...)
	ctx := &fieldContext{reg: reg}
	for _, o := range e.options {
		// Registry options are validated at registration time; a failure here
		// surfaces through the field's own option application.
		_ = o.ApplyFieldOption(ctx)
	}
}

// Semantic kind overrides for [RegisterType].
func kindOption(k TypeKind) TypeOption {
	return typeOptionFunc(func(e *typeEntry) error {
		e.typ.Kind = k
		e.kindSet = true
		return nil
	})
}

// BooleanType declares boolean semantics.
func BooleanType() TypeOption { return kindOption(TypeBoolean) }

// IntegerType declares integer semantics.
func IntegerType() TypeOption { return kindOption(TypeInteger) }

// NumberType declares floating point semantics.
func NumberType() TypeOption { return kindOption(TypeNumber) }

// StringType declares string semantics.
func StringType() TypeOption { return kindOption(TypeString) }

// DurationType declares duration semantics.
func DurationType() TypeOption { return kindOption(TypeDuration) }

// TimestampType declares timestamp semantics.
func TimestampType() TypeOption { return kindOption(TypeTimestamp) }

// Constrain attaches a constraint to every field of the type.
func Constrain(c Constraint) TypeOption {
	return typeOptionFunc(func(e *typeEntry) error {
		if c == nil {
			return errors.New("nil constraint")
		}
		e.constraints = append(e.constraints, c)
		return nil
	})
}

// InRange constrains every field of the type to an inclusive range.
func InRange(minimum, maximum any) TypeOption {
	return Constrain(RangeConstraint{Minimum: minimum, Maximum: maximum})
}

// TypeFieldOptions applies field options to every field of the type, such as
// source decoders or accepted shapes.
func TypeFieldOptions(opts ...FieldOption) TypeOption {
	return typeOptionFunc(func(e *typeEntry) error {
		e.options = append(e.options, opts...)
		return nil
	})
}
