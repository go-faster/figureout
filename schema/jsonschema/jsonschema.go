// Package jsonschema emits JSON Schema from a configuration descriptor.
//
// A schema describes either the semantic configuration values or the
// representation accepted by one source. The projection is always explicit:
// see [Semantic] and [ForSource].
package jsonschema

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/go-faster/figureout"
)

// Target identifies the JSON Schema target.
const Target = figureout.TargetID("jsonschema")

// Dialect is the emitted JSON Schema dialect.
const Dialect = "https://json-schema.org/draft/2020-12/schema"

// JSON Schema keywords used often enough to name.
const (
	keyType       = "type"
	keyProperties = "properties"
	keyRequired   = "required"
	typeInteger   = "integer"
)

// Diagnostic codes reported by generation.
const (
	// CodeNotRepresentable reports a semantic rule that JSON Schema cannot
	// express, so that generation never silently implies full coverage.
	CodeNotRepresentable = "target.not_representable"
	// CodeOverride reports a structural override applied by [Override].
	CodeOverride = "target.override"
)

// Option customizes generation.
type Option interface {
	applyJSONSchema(*generator) error
}

type optionFunc func(*generator) error

func (f optionFunc) applyJSONSchema(g *generator) error { return f(g) }

// Semantic emits a schema of the semantic configuration values. It is the
// default projection.
func Semantic() Option {
	return optionFunc(func(g *generator) error {
		g.source = ""
		return nil
	})
}

// ForSource emits a schema of the representation accepted by one source.
func ForSource(id figureout.SourceID) Option {
	return optionFunc(func(g *generator) error {
		g.source = id
		return nil
	})
}

// StrictExport turns "cannot be represented" reports into errors.
func StrictExport() Option {
	return optionFunc(func(g *generator) error {
		g.strict = true
		return nil
	})
}

// Title sets the schema title.
func Title(title string) Option {
	return optionFunc(func(g *generator) error {
		g.title = title
		return nil
	})
}

// Patch is additive or structural JSON Schema metadata.
type Patch struct {
	Title       *string
	Description *string
	Examples    []any
	Format      *string
	Type        []string
}

type patch struct {
	Patch
	structural bool
}

// Decorate attaches JSON Schema metadata. It rejects changes to derived
// structural properties such as type.
func Decorate(p Patch) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		if len(p.Type) > 0 {
			return errDecorateStructural
		}
		return c.AddTargetAnnotation(Target, patch{Patch: p})
	})
}

// Override replaces derived structural properties. It is deliberately
// separate from [Decorate]: a conflict with source codecs or semantic
// constraints is reported during generation.
func Override(p Patch) figureout.FieldOption {
	return figureout.FieldOptionFunc(func(c figureout.FieldOptionContext) error {
		return c.AddTargetAnnotation(Target, patch{Patch: p, structural: true})
	})
}

type generatorError string

func (e generatorError) Error() string { return string(e) }

const errDecorateStructural generatorError = "Decorate cannot change structural properties; use Override"

// Generate emits a JSON Schema document for the descriptor.
func Generate[T any](d *figureout.Descriptor[T], opts ...Option) ([]byte, figureout.Diagnostics, error) {
	g := &generator{}
	for _, o := range opts {
		if err := o.applyJSONSchema(g); err != nil {
			return nil, nil, err
		}
	}

	doc := g.object(d.Model().Root)
	doc["$schema"] = Dialect
	if g.title != "" {
		doc["title"] = g.title
	}
	if g.strict {
		for i := range g.diags {
			g.diags[i].Severity = figureout.SeverityError
		}
	}
	if err := g.diags.Err(); err != nil {
		return nil, g.diags, err
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, g.diags, err
	}
	return append(out, '\n'), g.diags, nil
}

type generator struct {
	source figureout.SourceID
	strict bool
	title  string
	diags  figureout.Diagnostics
}

func (g *generator) object(o *figureout.ObjectModel) map[string]any {
	props := map[string]any{}
	var required []string

	for _, f := range o.Fields {
		props[f.Name] = g.field(f)
		if f.Presence == figureout.PresenceRequired && !applied(f) {
			required = append(required, f.Name)
		}
	}

	doc := map[string]any{
		keyType:                "object",
		keyProperties:          props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		doc[keyRequired] = required
	}
	return doc
}

func applied(f *figureout.FieldModel) bool {
	return f.Default != nil && f.Default.Applied
}

// erasable reports whether an explicit null leaves the field resolvable: it
// must be optional, or have a default to fall back to.
func erasable(f *figureout.FieldModel) bool {
	return f.Presence != figureout.PresenceRequired || applied(f)
}

func (g *generator) field(f *figureout.FieldModel) map[string]any {
	var doc map[string]any
	switch {
	case f.Type.Union != nil:
		doc = g.union(f)
	case f.Type.Object != nil:
		doc = g.object(f.Type.Object)
	default:
		doc = g.scalar(f)
	}

	// A unit-scaled duration is an integer on the wire, and the unit is the
	// only thing that says what the integer counts.
	if description := describeUnit(f.Meta.Doc, f.Type.Unit); description != "" {
		doc["description"] = description
	}
	if f.Meta.Deprecated != "" {
		doc["deprecated"] = true
	}
	if len(f.Meta.Examples) > 0 {
		doc["examples"] = wireValues(f.Type, f.Meta.Examples)
	}
	if f.Default != nil {
		doc["default"] = wireValue(f.Type, f.Default.Value)
	}
	g.applyPatches(f, doc)
	return doc
}

func (g *generator) union(f *figureout.FieldModel) map[string]any {
	u := f.Type.Union
	variants := make([]any, 0, len(u.Variants))
	for _, v := range u.Variants {
		schema := g.object(v.Object)
		props, _ := schema[keyProperties].(map[string]any)
		props[u.Discriminator] = map[string]any{
			"type":  "string",
			"const": v.Tag,
		}
		required, _ := schema[keyRequired].([]string)
		schema[keyRequired] = append([]string{u.Discriminator}, required...)
		variants = append(variants, schema)
	}
	return map[string]any{
		"oneOf": variants,
		"discriminator": map[string]any{
			"propertyName": u.Discriminator,
		},
	}
}

func (g *generator) scalar(f *figureout.FieldModel) map[string]any {
	doc := map[string]any{}
	types := g.types(f)
	switch {
	case len(types) == 1:
		doc[keyType] = types[0]
	case len(types) > 1:
		doc[keyType] = types
	}

	if format := formatOfType(f.Type); format != "" {
		doc["format"] = format
	}
	if f.Type.Kind == figureout.TypeBytes {
		doc["contentEncoding"] = "base64"
	}
	if f.Type.Kind == figureout.TypeList && f.Type.Elem != nil {
		doc["items"] = g.typeSchema(*f.Type.Elem)
	}
	if f.Type.Kind == figureout.TypeMap && f.Type.Elem != nil {
		doc["additionalProperties"] = g.typeSchema(*f.Type.Elem)
	}

	g.constraints(f, doc)
	return doc
}

// types returns the JSON types accepted for the field under the selected
// projection.
func (g *generator) types(f *figureout.FieldModel) []string {
	var out []string
	if g.source != "" {
		if p, ok := f.Source(g.source); ok {
			for _, shape := range p.DeriveShapes(f.Type) {
				out = appendUnique(out, shape.Kind.String())
			}
		}
	}
	if len(out) == 0 {
		out = []string{jsonTypeOf(f.Type)}
	}
	// Null is a merge directive rather than a value: a source spells it to
	// erase what earlier layers set. It therefore belongs in a schema that
	// describes what a source accepts, never in the semantic schema, and only
	// where erasing leaves the field with something to fall back on.
	if g.source != "" && erasable(f) {
		out = appendUnique(out, "null")
	}
	return out
}

func appendUnique(dst []string, v string) []string {
	for _, e := range dst {
		if e == v {
			return dst
		}
	}
	return append(dst, v)
}

func (g *generator) typeSchema(t figureout.Type) map[string]any {
	doc := map[string]any{keyType: jsonType(t.Kind)}
	if format := formatOf(t.Kind); format != "" {
		doc["format"] = format
	}
	if t.Kind == figureout.TypeList && t.Elem != nil {
		doc["items"] = g.typeSchema(*t.Elem)
	}
	return doc
}

func jsonType(k figureout.TypeKind) string {
	switch k {
	case figureout.TypeBoolean:
		return "boolean"
	case figureout.TypeInteger:
		return typeInteger
	case figureout.TypeNumber:
		return "number"
	case figureout.TypeString, figureout.TypeBytes, figureout.TypeDuration, figureout.TypeTimestamp:
		return "string"
	case figureout.TypeList:
		return "array"
	default:
		return "object"
	}
}

func formatOf(k figureout.TypeKind) string {
	switch k {
	case figureout.TypeDuration:
		return "duration"
	case figureout.TypeTimestamp:
		return "date-time"
	default:
		return ""
	}
}

// jsonTypeOf is [jsonType] aware of a declared unit, which turns a duration
// into the integer it is actually written as.
func jsonTypeOf(t figureout.Type) string {
	if t.Kind == figureout.TypeDuration && t.Unit > 0 {
		return typeInteger
	}
	return jsonType(t.Kind)
}

func formatOfType(t figureout.Type) string {
	if t.Kind == figureout.TypeDuration && t.Unit > 0 {
		return ""
	}
	return formatOf(t.Kind)
}

// describeUnit names what a unit-scaled integer counts, keeping any
// documentation the field already carries.
func describeUnit(doc string, unit time.Duration) string {
	if unit <= 0 {
		return doc
	}
	in := "In " + figureout.Type{Unit: unit}.UnitName() + "."
	if doc == "" {
		return in
	}
	return doc + " " + in
}

// wireValue renders a semantic value the way the field is written on the wire.
//
// A duration is nanoseconds in Go and either a duration string or a count of
// its unit in a document; emitting the Go number would document a value no
// source would accept.
func wireValue(t figureout.Type, v any) any {
	if t.Kind != figureout.TypeDuration {
		return v
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || !rv.CanConvert(durationType) {
		return v
	}
	d := time.Duration(rv.Convert(durationType).Int())
	if t.Unit > 0 {
		return int64(d / t.Unit)
	}
	return d.String()
}

func wireValues(t figureout.Type, values []any) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = wireValue(t, v)
	}
	return out
}

var durationType = reflect.TypeFor[time.Duration]()

func (g *generator) constraints(f *figureout.FieldModel, doc map[string]any) {
	for _, c := range f.Constraints {
		switch c := c.(type) {
		case figureout.RangeConstraint:
			// A duration or timestamp is a string on the wire, where numeric
			// bounds have no meaning. Report rather than emit a keyword that
			// no validator would apply.
			if wire := jsonTypeOf(f.Type); wire != typeInteger && wire != "number" {
				g.diags = append(g.diags, figureout.Diagnostic{
					Severity:  figureout.SeverityWarning,
					Code:      CodeNotRepresentable,
					Target:    Target,
					FieldPath: f.Path,
					GoPath:    f.GoName,
					Message: "range constraint on a " + f.Type.Kind.String() +
						" cannot be represented in JSON Schema",
				})
				continue
			}
			if c.Minimum != nil {
				doc[minimumKey(c.ExclusiveMinimum)] = wireValue(f.Type, c.Minimum)
			}
			if c.Maximum != nil {
				doc[maximumKey(c.ExclusiveMaximum)] = wireValue(f.Type, c.Maximum)
			}
		case figureout.LengthConstraint:
			minKey, maxKey := "minLength", "maxLength"
			if f.Type.Kind == figureout.TypeList {
				minKey, maxKey = "minItems", "maxItems"
			}
			if c.Minimum != nil {
				doc[minKey] = *c.Minimum
			}
			if c.Maximum != nil {
				doc[maxKey] = *c.Maximum
			}
		case figureout.EnumConstraint:
			doc["enum"] = wireValues(f.Type, c.Values)
		case figureout.PatternConstraint:
			doc["pattern"] = c.Expression
		case figureout.CheckConstraint:
			g.diags = append(g.diags, figureout.Diagnostic{
				Severity:  figureout.SeverityWarning,
				Code:      figureout.CodeValidatorNotExport,
				Target:    Target,
				FieldPath: f.Path,
				GoPath:    f.GoName,
				Message:   "validator " + c.Name + " cannot be represented in JSON Schema",
			})
		}
	}
}

func minimumKey(exclusive bool) string {
	if exclusive {
		return "exclusiveMinimum"
	}
	return "minimum"
}

func maximumKey(exclusive bool) string {
	if exclusive {
		return "exclusiveMaximum"
	}
	return "maximum"
}

func (g *generator) applyPatches(f *figureout.FieldModel, doc map[string]any) {
	for _, a := range f.Targets[Target] {
		p, ok := a.(patch)
		if !ok {
			continue
		}
		if p.Title != nil {
			doc["title"] = *p.Title
		}
		if p.Description != nil {
			doc["description"] = *p.Description
		}
		if len(p.Examples) > 0 {
			doc["examples"] = p.Examples
		}
		if p.Format != nil {
			doc["format"] = *p.Format
		}
		if len(p.Type) > 0 {
			if !p.structural {
				continue
			}
			doc[keyType] = p.Type
			g.diags = append(g.diags, figureout.Diagnostic{
				Severity:  figureout.SeverityInfo,
				Code:      CodeOverride,
				Target:    Target,
				FieldPath: f.Path,
				GoPath:    f.GoName,
				Message:   "structural type was overridden",
			})
		}
	}
}
