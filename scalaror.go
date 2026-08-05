package figureout

import (
	"reflect"
	"strings"
	"unsafe"

	"github.com/go-faster/errors"
)

// ScalarOr registers a nested object that may also be written as a scalar.
//
// "A scalar, or an object" is one of the most common configuration idioms, and
// [OneOf] cannot express it: a union needs a discriminator, and a bare scalar
// has nowhere to put one. Without ScalarOr every occurrence is a [WithDecoder]
// plus hand-written [Shape] values that duplicate the descriptor already
// describing the same thing, and drift the moment a field is added to it.
//
//	figureout.ScalarOr(s, &c.AuthToken, "auth_token", secretDescriptor,
//		func(v string) Secret { return Secret{Value: v} })
//
//	auth_token: sk-live-...          # widened by the function
//	auth_token: {file: /run/token}   # decoded by the descriptor
//
// The accepted shapes are derived from the descriptor and from S, so they
// cannot drift: generated schemas emit oneOf over the two, and a source that
// has no object syntax — environment variables, mounted files — accepts the
// scalar spelling directly at the object's own name.
//
// A scalar and an object spelling never combine across layers: whichever a
// later layer uses replaces the other outright, because a widened value and a
// half-filled object have no meaningful merge.
func ScalarOr[R, C, S any](
	s *Schema[R],
	field *C,
	name string,
	d *Descriptor[C],
	widen func(S) C,
	opts ...FieldOption,
) *ObjectField {
	b := s.b
	reg := b.register(unsafe.Pointer(field), reflect.TypeFor[C](), name, regObject)
	if reg.valid {
		switch {
		case d == nil:
			b.diags.errorf(CodeMissingDefinition, reg.goName, name,
				"nil descriptor for nested object %q", name)
		case widen == nil:
			b.diags.errorf(CodeMissingDefinition, reg.goName, name,
				"nil widening function for %q", name)
		case reg.acc.presence != PresenceRequired:
			b.diags.errorf(CodeUnsupportedType, reg.goName, name,
				"nested objects do not support %s presence yet", reg.acc.presence)
		default:
			scalar, ok := b.deriveType(reflect.TypeFor[S]())
			switch {
			case !ok:
				b.diags.errorf(CodeUnsupportedType, reg.goName, name,
					"cannot derive a semantic type for %s", reflect.TypeFor[S]())
			case scalar.Kind == TypeObject || scalar.Kind == TypeUnion:
				b.diags.errorf(CodeUnsupportedType, reg.goName, name,
					"the alternative spelling of %q must be a scalar, not a %s", name, scalar.Kind)
			default:
				reg.object = d.model.Root
				reg.typ = Type{
					Kind:   TypeObject,
					Go:     reg.acc.elem,
					Object: d.model.Root,
					Scalar: &scalar,
				}
				reg.widen = widening(widen)
			}
		}
	}
	b.applyOptions(reg, opts)
	return &ObjectField{&FieldBuilder{b: b, reg: reg}}
}

// widening adapts a typed widening function to the untyped one the model holds.
func widening[C, S any](widen func(S) C) func(any) (any, error) {
	return func(v any) (any, error) {
		s, ok := v.(S)
		if !ok {
			rv := reflect.ValueOf(v)
			if !rv.IsValid() || !rv.CanConvert(reflect.TypeFor[S]()) {
				return nil, errors.Errorf("cannot use %T as %s", v, reflect.TypeFor[S]())
			}
			s = rv.Convert(reflect.TypeFor[S]()).Interface().(S)
		}
		return widen(s), nil
	}
}

// dropOtherSpelling clears whichever spelling of a [ScalarOr] field the
// incoming assignment is not.
//
// Setting the object's own path drops the members an earlier layer wrote;
// setting a member drops an earlier layer's widened scalar. Without this, a
// later layer's object would be materialized on top of an earlier scalar that
// already stands for the whole value.
func (m *Model) dropOtherSpelling(state map[string]*merged, path string) {
	if f, ok := m.byPath[path]; ok {
		if _, shorthand := f.Shorthand(); shorthand {
			prefix := path + "."
			for p := range state {
				if strings.HasPrefix(p, prefix) {
					delete(state, p)
				}
			}
			return
		}
	}

	// Walk up to the nearest enclosing shorthand object, if any.
	for i := strings.LastIndex(path, "."); i > 0; i = strings.LastIndex(path[:i], ".") {
		f, ok := m.byPath[path[:i]]
		if !ok {
			continue
		}
		if _, shorthand := f.Shorthand(); shorthand {
			delete(state, path[:i])
			return
		}
	}
}

// Shorthand reports whether the field accepts a scalar in place of its object,
// and returns the scalar type it accepts.
//
// Sources without an object syntax use it to bind the scalar spelling at the
// object's own name.
func (f *FieldModel) Shorthand() (Type, bool) {
	if f.Type.Kind != TypeObject || f.Type.Scalar == nil {
		return Type{}, false
	}
	return *f.Type.Scalar, true
}
