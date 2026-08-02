// Package tree is the shared document model for hierarchical sources.
//
// JSON, YAML and TOML remain separate adapters because they differ in scalar
// syntax, null handling and numeric precision. What they share is structure:
// each parses a document into a tree of objects, arrays and scalars carrying
// source positions, and each binds that tree onto the same descriptor. This
// package holds only that shared part; every format-specific decision stays
// with its adapter, behind [ScalarDecoder].
package tree

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

// Field is one member of an object node.
type Field struct {
	Key   string
	Pos   Pos
	Value *Node
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
