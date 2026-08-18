package figureout

import (
	"reflect"
	"unsafe"
)

// Opaque registers a subtree carried verbatim, whose shape belongs to another
// program.
//
// A configuration that embeds another program's configuration has a block it
// cannot describe and must not validate:
//
//	// an OpenTelemetry Collector configuration, handed to the collector as-is
//	Collector map[string]any `yaml:"otelcol"`
//
//	figureout.Opaque(s, &c.Collector, "otelcol",
//		figureout.Reason("handed to the collector verbatim"))
//
// The field decodes to whatever the document held — objects as map[string]any,
// arrays as []any, scalars as the format resolved them — and its whole subtree
// is exempt from [DisallowUnknownFields]. That exemption is the load-bearing
// part: strict decoding is why a descriptor is worth adopting, and a
// passthrough is precisely where strictness has to stop, because figureout
// cannot know which keys the other program accepts and a version skew in *that*
// program is not this one's business.
//
// It is therefore a deliberate hole, and [Reason] is required so that it reads
// as one at the declaration site. The reason is documentation: generated
// schemas describe a permissive object carrying it, rather than omitting the
// field.
//
// Absence resolves to the zero value, as [Value] does; [FieldBuilder.Required]
// opts back in. A source with no nesting, such as environment variables or
// mounted files, skips the field the way it skips a collection of objects.
//
// Opaque is not an escape hatch for a block that could be described. Whatever
// is inside it has no names, no constraints, no defaults, no provenance and no
// schema — reach for [ObjectFunc] wherever the shape is yours to state.
func Opaque[R, C any](s *Schema[R], field *C, name string, opts ...FieldOption) *OpaqueField {
	b := s.b
	reg := b.register(unsafe.Pointer(field), reflect.TypeFor[C](), name, regOpaque)
	b.applyOptions(reg, opts)
	if reg.valid && reg.reason == "" {
		b.diags.errorf(CodeMissingDefinition, reg.goName, name,
			"opaque field %q needs a Reason: it takes its whole subtree out of "+
				"unknown-field checking, which is a hole and has to read as one",
			name)
	}
	return &OpaqueField{&FieldBuilder{b: b, reg: reg}}
}

// OpaqueField is the fluent builder for a passthrough subtree.
//
// It carries no constraints and no defaults: there is nothing described to
// constrain, and a default would be this program's opinion about another one's
// configuration.
type OpaqueField struct{ *FieldBuilder }

// Doc attaches documentation.
func (f *OpaqueField) Doc(text string) *OpaqueField {
	f.FieldBuilder.Doc(text)
	return f
}

// Required makes an absent passthrough an error instead of the zero value.
func (f *OpaqueField) Required() *OpaqueField {
	f.FieldBuilder.Required()
	return f
}

// Opaque reports whether the field carries a subtree verbatim, and why.
//
// Targets use it to describe a passthrough rather than to describe what is
// inside it, which nothing knows.
func (f *FieldModel) Opaque() (reason string, ok bool) {
	if f.Type.Kind != TypeOpaque {
		return "", false
	}
	return f.reason, true
}
