package figureout

import (
	"reflect"
	"slices"
	"unsafe"

	"github.com/go-faster/errors"
)

// UnionOption declares part of a union.
type UnionOption interface {
	applyUnion(*unionBuilder) error
}

type unionBuilder struct {
	b             *builder
	reg           *registration
	discriminator string
	variants      []*VariantModel
}

type unionOptionFunc func(*unionBuilder) error

func (f unionOptionFunc) applyUnion(u *unionBuilder) error { return f(u) }

// Discriminator names the property carrying the variant tag. It is required.
func Discriminator(name string) UnionOption {
	return unionOptionFunc(func(u *unionBuilder) error {
		if name == "" {
			return errors.New("empty discriminator name")
		}
		u.discriminator = name
		return nil
	})
}

// Variant declares one alternative of a union.
//
// The variant field must be a pointer to the variant struct: the pointer being
// non-nil is what records which variant was selected.
func Variant[V any](tag string, field **V, d *Descriptor[V]) UnionOption {
	return unionOptionFunc(func(u *unionBuilder) error {
		if tag == "" {
			return errors.New("empty variant tag")
		}
		if d == nil {
			return errors.Errorf("variant %q: nil descriptor", tag)
		}

		bd, err := u.b.bind.resolve(unsafe.Pointer(field), reflect.TypeFor[*V](), tag)
		if err != nil {
			return err
		}
		if !slices.Equal(bd.index[:len(bd.index)-1], u.reg.bound.index) {
			return errors.Errorf("variant %q: %s is not a field of %s", tag, bd.goPath, u.reg.goName)
		}

		u.variants = append(u.variants, &VariantModel{
			Tag:    tag,
			GoPath: FieldPath{Index: bd.index, Offset: bd.offset},
			Object: d.model.Root,
			acc: accessor{
				index:    bd.index,
				presence: PresenceRequired,
				elem:     reflect.TypeFor[*V](),
				settable: !bd.skipped,
			},
		})
		return nil
	})
}

// OneOf registers a tagged union: a field whose shape is selected by a
// discriminator property.
//
// A union is a sum of alternative shapes. To restrict one scalar to a set of
// values, use [Enum] instead.
//
//	figureout.OneOf(s, &c.Backend, "backend",
//		figureout.Discriminator("type"),
//		figureout.Variant("s3", &c.Backend.S3, S3Descriptor),
//		figureout.Variant("local", &c.Backend.Local, LocalDescriptor),
//	)
func OneOf[R, C any](s *Schema[R], field *C, name string, opts ...UnionOption) *UnionField {
	b := s.b
	reg := b.register(unsafe.Pointer(field), reflect.TypeFor[C](), name, regUnion)
	if reg.goName == "" {
		return &UnionField{&FieldBuilder{b: b, reg: reg}}
	}

	u := &unionBuilder{b: b, reg: reg}
	for _, o := range opts {
		if err := o.applyUnion(u); err != nil {
			b.diags.errorf(CodeUnionInvalid, reg.goName, name, "%s", err)
		}
	}

	switch {
	case u.discriminator == "":
		b.diags.errorf(CodeUnionInvalid, reg.goName, name, "union %q has no discriminator", name)
	case len(u.variants) == 0:
		b.diags.errorf(CodeUnionInvalid, reg.goName, name, "union %q has no variants", name)
	default:
		seen := map[string]struct{}{}
		for _, v := range u.variants {
			if _, dup := seen[v.Tag]; dup {
				b.diags.errorf(CodeUnionInvalid, reg.goName, name, "duplicate variant tag %q", v.Tag)
			}
			seen[v.Tag] = struct{}{}
		}
		reg.union = &Union{Discriminator: u.discriminator, Variants: u.variants}
		reg.typ = Type{Kind: TypeUnion, Go: reg.acc.elem, Union: reg.union}
	}
	return &UnionField{&FieldBuilder{b: b, reg: reg}}
}
