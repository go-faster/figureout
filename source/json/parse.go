package json

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout/internal/tree"
)

// parse builds a document tree with a position on every node.
//
// The standard decoder is used rather than a faster one because its
// InputOffset is public: exact offsets are what let a diagnostic point at
// file:line:column instead of only naming the property.
func parse(src []byte) (*tree.Node, error) {
	p := &parser{
		src: src,
		dec: json.NewDecoder(bytes.NewReader(src)),
	}
	p.dec.UseNumber()
	p.indexLines()

	root, err := p.value()
	if err != nil {
		return nil, err
	}
	if _, err := p.dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after top-level value")
	}
	return root, nil
}

type parser struct {
	src []byte
	dec *json.Decoder
	// lineStarts holds the offset of every line's first byte.
	lineStarts []int
}

func (p *parser) indexLines() {
	p.lineStarts = []int{0}
	for i, c := range p.src {
		if c == '\n' {
			p.lineStarts = append(p.lineStarts, i+1)
		}
	}
}

// pos converts a byte offset to a one-based line and column.
func (p *parser) pos(offset int) tree.Pos {
	if offset < 0 {
		offset = 0
	}
	if offset > len(p.src) {
		offset = len(p.src)
	}
	// BinarySearch returns the insertion point when the offset is not itself
	// a line start, so the containing line is the one before it.
	line, exact := slices.BinarySearch(p.lineStarts, offset)
	if !exact {
		line--
	}
	return tree.Pos{Line: line + 1, Col: offset - p.lineStarts[line] + 1}
}

// next returns the offset of the next value, skipping the separators the
// token stream hides.
func (p *parser) next() int {
	i := int(p.dec.InputOffset())
	for i < len(p.src) {
		switch p.src[i] {
		case ' ', '\t', '\r', '\n', ',', ':':
			i++
		default:
			return i
		}
	}
	return i
}

func (p *parser) value() (*tree.Node, error) {
	pos := p.pos(p.next())
	tok, err := p.dec.Token()
	if err != nil {
		return nil, err
	}

	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return p.object(pos)
		case '[':
			return p.array(pos)
		default:
			return nil, errors.Errorf("unexpected %q", t)
		}
	case nil:
		return &tree.Node{Kind: tree.Null, Pos: pos, Text: "null"}, nil
	case bool:
		return &tree.Node{Kind: tree.Scalar, Pos: pos, Value: t, Tag: tagBool}, nil
	case string:
		return &tree.Node{Kind: tree.Scalar, Pos: pos, Value: t, Text: t, Tag: tagString}, nil
	case json.Number:
		return &tree.Node{Kind: tree.Scalar, Pos: pos, Value: t, Text: t.String(), Tag: tagNumber}, nil
	default:
		return nil, errors.Errorf("unsupported token %T", tok)
	}
}

func (p *parser) object(pos tree.Pos) (*tree.Node, error) {
	node := &tree.Node{Kind: tree.Object, Pos: pos}
	for p.dec.More() {
		keyPos := p.pos(p.next())
		keyTok, err := p.dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, errors.Errorf("object key must be a string, got %T", keyTok)
		}

		value, err := p.value()
		if err != nil {
			return nil, err
		}
		node.Fields = append(node.Fields, tree.Field{Key: key, Pos: keyPos, Value: value})
	}
	_, err := p.dec.Token() // closing brace
	return node, err
}

func (p *parser) array(pos tree.Pos) (*tree.Node, error) {
	node := &tree.Node{Kind: tree.Array, Pos: pos}
	for p.dec.More() {
		item, err := p.value()
		if err != nil {
			return nil, err
		}
		node.Items = append(node.Items, item)
	}
	_, err := p.dec.Token() // closing bracket
	return node, err
}

// JSON type tags, mirroring the shapes a JSON Schema would name.
const (
	tagBool   = "boolean"
	tagNumber = "number"
	tagString = "string"
)
