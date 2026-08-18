package docs

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/figureout"
)

type generator struct {
	model   *figureout.Model
	title   string
	strict  bool
	sources []*Source
	unnamed []figureout.SourceID
	diags   figureout.Diagnostics

	out     *Page
	anchors map[string]int
}

// rootTitle names the section documenting the descriptor's own object, which
// has no path to be named after.
const rootTitle = "Configuration"

func (g *generator) page() *Page {
	g.out = &Page{Title: g.title}
	for _, s := range g.sources {
		g.out.Sources = append(g.out.Sources, s.ID)
	}
	g.anchors = map[string]int{}
	g.section(g.model.Root, "", g.newSection(rootTitle))
	return g.out
}

// newSection reserves the section's anchor as it is created, so a link made
// before the section is walked is the link the heading gets.
func (g *generator) newSection(title string) *Section {
	return &Section{Title: title, Anchor: g.anchor(title)}
}

// section documents one object. base is the canonical path its fields hang
// off, empty at the root.
func (g *generator) section(obj *figureout.ObjectModel, base string, s *Section) {
	g.out.Sections = append(g.out.Sections, s)

	// Nested sections are appended after this one is complete, so a reader
	// meets an object before its members.
	var nested []func()

	if s.Discriminator != "" {
		s.Fields = append(s.Fields, g.discriminator(s, base))
	}
	for _, f := range obj.Fields {
		if f.Meta.Hidden {
			continue
		}
		row := g.field(f)
		s.Fields = append(s.Fields, row)

		if f.Moved() {
			// A former spelling documents itself as deprecated and points at
			// what superseded it; the structure is documented there.
			continue
		}
		switch elem, collection := f.Elements(); {
		case f.Type.Union != nil:
			for _, v := range f.Type.Union.Variants {
				variant := g.newSection(f.Path + " (" + f.Type.Union.Discriminator + ": " + v.Tag + ")")
				variant.Path = f.Path
				variant.Doc = f.Meta.Doc
				variant.Discriminator = f.Type.Union.Discriminator
				variant.Variant = v.Tag
				if row.Section == "" {
					row.Section = variant.Anchor
				}
				nested = append(nested, func() { g.section(v.Object, f.Path+".", variant) })
			}
		case collection:
			element := g.newSection(figureout.ElementPath(f.Path, ""))
			element.Path = f.Path
			element.Doc = f.Meta.Doc
			row.Section = element.Anchor
			nested = append(nested, func() { g.section(elem, element.Title+".", element) })
		case f.Type.Object != nil:
			object := g.newSection(f.Path)
			object.Path = f.Path
			object.Doc = f.Meta.Doc
			object.Deprecated = f.Meta.Deprecated
			row.Section = object.Anchor
			nested = append(nested, func() { g.section(f.Type.Object, f.Path+".", object) })
		}
	}

	for _, run := range nested {
		run()
	}
}

// discriminator documents the union tag, which selects the variant and is a
// configuration value in its own right without being a Go field.
func (g *generator) discriminator(s *Section, base string) *Field {
	path := strings.TrimSuffix(base, ".") + "." + s.Discriminator
	return &Field{
		Name:     s.Discriminator,
		Path:     path,
		Type:     "string",
		Required: true,
		Values:   []string{s.Variant},
		Doc:      "Selects this variant.",
		Names:    g.names(path),
	}
}

func (g *generator) field(f *figureout.FieldModel) *Field {
	row := &Field{
		Name:       f.Name,
		Path:       f.Path,
		Type:       typeName(f.Type),
		Required:   f.Required(),
		Doc:        f.Meta.Doc,
		Deprecated: f.Meta.Deprecated,
		MovedTo:    f.MovedTo,
		Secret:     f.Meta.Secret,
		Names:      g.names(f.Path),
	}
	if f.Default != nil {
		row.Default = g.value(f, f.Default.Value)
		row.DefaultApplied = f.Default.Applied
	}
	for _, v := range f.Meta.Examples {
		row.Examples = append(row.Examples, g.value(f, v))
	}
	if values, ok := figureout.EnumValuesOf(f); ok {
		for _, v := range values {
			row.Values = append(row.Values, g.value(f, v))
		}
	}
	row.Constraints = g.constraints(f)
	return row
}

func (g *generator) names(path string) map[figureout.SourceID][]string {
	var out map[figureout.SourceID][]string
	for _, s := range g.sources {
		names, ok := s.Names(path)
		if !ok {
			continue
		}
		if out == nil {
			out = map[figureout.SourceID][]string{}
		}
		out[s.ID] = names
	}
	return out
}

// value renders a semantic value the way the field is written on the wire. A
// secret is never rendered: a documented credential is a leaked one.
func (g *generator) value(f *figureout.FieldModel, v any) string {
	if f.Meta.Secret {
		return "[redacted]"
	}
	return formatValue(f.Type, v)
}

func (g *generator) constraints(f *figureout.FieldModel) []string {
	var out []string
	for _, c := range f.Constraints {
		switch c := c.(type) {
		case figureout.RangeConstraint:
			if s := rangeProse(f.Type, c); s != "" {
				out = append(out, s)
			}
		case figureout.LengthConstraint:
			if s := lengthProse(f.Type, c); s != "" {
				out = append(out, s)
			}
		case figureout.PatternConstraint:
			out = append(out, "matches "+c.Expression)
		case figureout.EnumConstraint:
			// Allowed values are documented as values, not as a rule.
		case figureout.CheckConstraint:
			// The name is documentable; the rule behind it is not, and saying
			// so is what keeps the page from implying it is exhaustive.
			out = append(out, "checked by "+c.Name)
			g.diags = append(g.diags, figureout.Diagnostic{
				Severity:  figureout.SeverityWarning,
				Code:      CodeNotDocumentable,
				Target:    Target,
				FieldPath: f.Path,
				GoPath:    f.GoName,
				Message:   "validator " + c.Name + " has no prose form; only its name is documented",
			})
		}
	}
	return out
}

func rangeProse(t figureout.Type, c figureout.RangeConstraint) string {
	var parts []string
	if c.Minimum != nil {
		parts = append(parts, boundProse("greater than", "at least", c.ExclusiveMinimum, t, c.Minimum))
	}
	if c.Maximum != nil {
		parts = append(parts, boundProse("less than", "at most", c.ExclusiveMaximum, t, c.Maximum))
	}
	return strings.Join(parts, ", ")
}

func boundProse(exclusive, inclusive string, isExclusive bool, t figureout.Type, v any) string {
	op := inclusive
	if isExclusive {
		op = exclusive
	}
	return op + " " + formatValue(t, v)
}

func lengthProse(t figureout.Type, c figureout.LengthConstraint) string {
	unit := "characters"
	switch t.Kind {
	case figureout.TypeBytes:
		unit = "bytes"
	case figureout.TypeList:
		unit = "items"
	case figureout.TypeMap:
		unit = "entries"
	}
	switch {
	case c.Minimum != nil && c.Maximum != nil:
		return fmt.Sprintf("between %d and %d %s", *c.Minimum, *c.Maximum, unit)
	case c.Minimum != nil:
		if *c.Minimum == 1 {
			return "non-empty"
		}
		return fmt.Sprintf("at least %d %s", *c.Minimum, unit)
	case c.Maximum != nil:
		return fmt.Sprintf("at most %d %s", *c.Maximum, unit)
	default:
		return ""
	}
}

// typeName names a semantic type in prose. A unit-scaled duration is named by
// what it is written as, because the unit is the only thing that says what the
// number counts.
func typeName(t figureout.Type) string {
	// A type that parses itself from text keeps its semantic kind and gains a
	// spelling, so the page names both rather than the one a reader would not
	// have guessed.
	if t.Text && t.Kind != figureout.TypeString {
		return t.Kind.String() + " or string"
	}
	switch t.Kind {
	case figureout.TypeDuration:
		if t.Unit > 0 {
			return "integer (" + t.UnitName() + ")"
		}
		return "duration"
	case figureout.TypeList:
		if t.Elem != nil {
			return "list of " + elemName(*t.Elem)
		}
		return "list"
	case figureout.TypeMap:
		if t.Key != nil && t.Elem != nil {
			return "map of " + elemName(*t.Key) + " to " + elemName(*t.Elem)
		}
		return "map"
	case figureout.TypeObject:
		if t.Scalar != nil {
			return typeName(*t.Scalar) + " or object"
		}
		return "object"
	default:
		return t.Kind.String()
	}
}

func elemName(t figureout.Type) string {
	if t.Object != nil {
		return "object"
	}
	return typeName(t)
}

// formatValue renders a semantic value the way a source writes it.
//
// A duration is nanoseconds in Go and either a duration string or a count of
// its unit in a document; printing the Go number would document a value no
// source accepts.
func formatValue(t figureout.Type, v any) string {
	if v == nil {
		return "null"
	}
	if t.Kind == figureout.TypeDuration {
		if rv := reflect.ValueOf(v); rv.IsValid() && rv.CanConvert(durationType) {
			d := time.Duration(rv.Convert(durationType).Int())
			if t.Unit > 0 {
				return strconv.FormatInt(int64(d/t.Unit), 10)
			}
			return d.String()
		}
	}

	if b, ok := v.([]byte); ok {
		return strconv.Quote(base64.StdEncoding.EncodeToString(b))
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return strconv.Quote(rv.String())
	case reflect.Slice, reflect.Array:
		if rv.Len() == 0 {
			return "[]"
		}
	case reflect.Map:
		if rv.Len() == 0 {
			return "{}"
		}
	}
	return fmt.Sprint(v)
}

var durationType = reflect.TypeFor[time.Duration]()

// anchor returns a unique anchor for a heading, so two objects that slug alike
// still get links of their own.
func (g *generator) anchor(title string) string {
	base := slug(title)
	n := g.anchors[base]
	g.anchors[base]++
	if n == 0 {
		return base
	}
	return base + "-" + strconv.Itoa(n)
}

// slug derives a heading anchor the way Markdown renderers do: lower case,
// spaces to hyphens, everything else that is not alphanumeric dropped.
func slug(title string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		case r == ' ':
			sb.WriteByte('-')
		}
	}
	return sb.String()
}
