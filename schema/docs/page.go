package docs

import (
	"github.com/go-faster/figureout"
)

// Page is a documentation document, rendered independently of any format.
//
// It is the model [Markdown] renders; a caller that wants another format walks
// it instead of walking the descriptor again.
type Page struct {
	Title string
	// Sources lists the documented sources, in the order [ForSource] named
	// them. Each contributes a column of names.
	Sources  []figureout.SourceID
	Sections []*Section
}

// Section documents one object: the root, a nested object, a collection
// element or one variant of a union.
type Section struct {
	// Title is the heading, which is the canonical path of the object.
	Title string
	// Anchor is the stable heading anchor, so a README can link to a field.
	Anchor string
	// Path is the canonical path of the object, empty for the root.
	Path string
	Doc  string
	// Deprecated is the reason the object is deprecated, if it is.
	Deprecated string
	// Discriminator and Variant are set when the section documents one
	// alternative of a union: the property carrying the tag, and the tag value
	// that selects this alternative.
	Discriminator string
	Variant       string

	Fields []*Field
}

// Field documents one row of a section.
type Field struct {
	// Name is the canonical name within the declaring object.
	Name string
	// Path is the canonical path from the descriptor root.
	Path string
	// Type is the semantic type in prose, such as "list of string".
	Type string
	// Required reports whether a source has to provide the field.
	Required bool
	// Default is the default value as it is written on the wire. It is empty
	// when the field has none.
	Default string
	// DefaultApplied distinguishes a default that changes the resolved value
	// from one recorded for documentation only.
	DefaultApplied bool
	// Values lists the allowed values of an enumerated field.
	Values []string
	// Constraints lists the declared rules in prose.
	Constraints []string
	// Examples lists example values as they are written on the wire.
	Examples []string
	Doc      string
	// Deprecated is the reason the field is deprecated, if it is.
	Deprecated string
	// MovedTo is the path superseding a former spelling.
	MovedTo string
	// Secret marks a credential, whose default and examples are redacted.
	Secret bool
	// Names holds the names each documented source accepts, primary first.
	Names map[figureout.SourceID][]string
	// Section is the anchor of the section documenting this field's own
	// object, empty for a field that is not one.
	Section string
}

// Source is a documented source: the names come from the source itself, so
// they cannot drift from what it reads.
type Source struct {
	ID    figureout.SourceID
	names map[string][]string
}

// Names returns the names the source accepts for a canonical field path.
func (s *Source) Names(path string) ([]string, bool) {
	names, ok := s.names[path]
	return names, ok
}
