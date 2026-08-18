// Package tree is the shared document model for hierarchical sources.
//
// JSON, YAML and TOML remain separate adapters because they differ in scalar
// syntax, null handling and numeric precision. What they share is structure:
// each parses a document into a tree of objects, arrays and scalars carrying
// source positions, and each binds that tree onto the same descriptor. This
// package holds only that shared part; every format-specific decision stays
// with its adapter, behind [ScalarDecoder].
package tree

import "github.com/go-faster/figureout"

// SchemaKey is the document member naming the schema that describes the file.
// It is accepted at the root of every document and bound to nothing.
const SchemaKey = "$schema"

// Kind is the structural kind of a document node.
type Kind uint8

// Node kinds.
const (
	Invalid Kind = iota
	Null
	Scalar
	Array
	Object
)

// String implements [fmt.Stringer].
func (k Kind) String() string {
	switch k {
	case Null:
		return "null"
	case Scalar:
		return "scalar"
	case Array:
		return "array"
	case Object:
		return "object"
	default:
		return "invalid"
	}
}

// Pos is a position within a source document.
type Pos struct {
	Line int
	Col  int
}

// Node is one value in a parsed document.
type Node struct {
	Kind Kind
	Pos  Pos

	// Value is the decoded scalar, for formats that decode scalars while
	// parsing. Adapters that keep scalars as text leave it nil and use Text.
	Value any
	// Text is the scalar's textual form.
	Text string
	// Tag is the format's own type marker, such as a YAML "!!int".
	Tag string

	Items  []*Node
	Fields []Field
}

// Raw returns the node as the format's natural Go representation, for a field
// that installed its own [figureout.Decoder].
//
// A scalar is whatever the adapter decoded while parsing, or its text for
// adapters that keep scalars as text; structure becomes []any and
// map[string]any. It is deliberately untyped: a decoder exists precisely to
// interpret a shape the semantic type does not describe.
func Raw(n *Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case Null:
		return nil
	case Array:
		out := make([]any, 0, len(n.Items))
		for _, item := range n.Items {
			out = append(out, Raw(item))
		}
		return out
	case Object:
		out := make(map[string]any, len(n.Fields))
		for _, f := range n.Fields {
			out[f.Key] = Raw(f.Value)
		}
		return out
	default:
		if n.Value != nil {
			return n.Value
		}
		return n.Text
	}
}

// ShapeOf returns the wire shape a node presents, so a field's declared
// [figureout.Shape] values can gate what reaches its decoder.
//
// A scalar reports [figureout.ShapeUnknown]: telling an integer from a string
// is the format's job, not the tree's, and a decoder that declares any scalar
// shape accepts it.
func ShapeOf(n *Node) figureout.ShapeKind {
	switch n.Kind {
	case Null:
		return figureout.ShapeNull
	case Array:
		return figureout.ShapeArray
	case Object:
		return figureout.ShapeObject
	default:
		return figureout.ShapeUnknown
	}
}

// Field is one member of an object node.
type Field struct {
	// Key is the member name, which is text: a path is format-neutral, and every
	// name a descriptor declares is spelled as one.
	Key string
	// KeyTag is the format's own type marker for the key, such as a YAML
	// "!!int". It matters only inside an opaque subtree, where the key is not a
	// name the descriptor declared but a value the other program will read.
	KeyTag string
	Pos    Pos
	Value  *Node
}

// Field looks up an object member by key. The last duplicate wins, matching
// what both JSON and YAML decoders do.
func (n *Node) Field(key string) (*Node, Pos, bool) {
	if n == nil || n.Kind != Object {
		return nil, Pos{}, false
	}
	for i := len(n.Fields) - 1; i >= 0; i-- {
		if n.Fields[i].Key == key {
			return n.Fields[i].Value, n.Fields[i].Pos, true
		}
	}
	return nil, Pos{}, false
}

// Keys returns the object's member names in document order.
func (n *Node) Keys() []string {
	if n == nil || n.Kind != Object {
		return nil
	}
	out := make([]string, 0, len(n.Fields))
	for _, f := range n.Fields {
		out = append(out, f.Key)
	}
	return out
}
