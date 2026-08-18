package tree

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-faster/errors"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/scalar"
)

// ScalarDecoder converts a scalar node into a semantic value of the field's Go
// type. It is the format-specific half of binding: JSON knows that 8080 is a
// number and "8080" is not, while YAML resolves the same text by tag.
type ScalarDecoder interface {
	// DecodeScalar converts n to a value of t. accepts carries the wire
	// shapes the field declared for this source, so a field that says it
	// takes a string as well as an integer decodes both; it is nil for
	// values that are not fields, such as list elements.
	DecodeScalar(t figureout.Type, n *Node, accepts []figureout.Shape) (any, error)

	// DecodeAny converts a whole node into the format's own untyped Go value,
	// for an opaque subtree no semantic type describes. Structure is the
	// format's here as much as scalars are — YAML picks a map[any]any as soon
	// as one key is not a string, JSON never does — so a passthrough carries
	// exactly what the program it is handed to would have read.
	DecodeAny(n *Node) (any, error)
}

// AliasOption adds accepted member names, tried after the primary name.
// Sources attach it through [figureout.FieldOptionContext.AddSourceOption].
type AliasOption struct {
	Names []string
}

// Binder maps a parsed document onto a descriptor, producing a layer.
type Binder struct {
	Source  figureout.SourceID
	File    string
	Decoder ScalarDecoder
	// DisallowUnknown reports object members that no field claims.
	DisallowUnknown bool
	// AllowNull permits explicit nulls. Formats without a null literal leave
	// it false.
	AllowNull bool
}

// Bind walks the document against the model.
func (b Binder) Bind(m *figureout.Model, root *Node) *figureout.Layer {
	layer := &figureout.Layer{Source: b.Source}
	if root == nil {
		return layer
	}
	if root.Kind != Object {
		b.errorf(layer, "", root.Pos, figureout.CodeSourceUnsupported,
			"document root must be an object, got %s", root.Kind)
		return layer
	}
	b.object(layer, m.Root, root, "", "", nil)
	return layer
}

// object binds an object node.
//
// base is the layer path this object contributes to, which is the field's model
// path everywhere except inside a collection, where it names one element.
// prefix is the document path, which follows the source's own names and drives
// provenance. claimed is seeded with member names already consumed by the
// caller, such as a union's discriminator.
func (b Binder) object(
	layer *figureout.Layer,
	obj *figureout.ObjectModel,
	node *Node,
	base string,
	prefix string,
	claimed map[string]struct{},
) {
	if claimed == nil {
		claimed = map[string]struct{}{}
	}

	for _, f := range obj.Fields {
		name, child, pos, ok := b.member(node, f)
		if !ok {
			continue
		}
		claimed[name] = struct{}{}
		docPath := prefix + name
		path := base + f.Name

		// A field that installed its own decoder owns every shape it declared,
		// including object and array ones the semantic type cannot describe.
		if dec, ok := decoderOf(f, b.Source); ok {
			b.decode(layer, f, dec, child, path, docPath, pos)
			continue
		}

		// A passthrough is bound whole and never descended into, which is what
		// exempts its subtree from the unknown-member report below.
		if f.Type.Kind == figureout.TypeOpaque {
			b.opaque(layer, f, child, path, docPath, pos)
			continue
		}

		switch elem, collection := f.Elements(); {
		case f.Type.Union != nil:
			b.union(layer, f, child, path, docPath)
		case f.Type.Object != nil:
			// Only a carrier can hold "no section": a required object is
			// materialized whether or not a document declares it, and a group
			// has no field of its own to be absent from. Which carrier it is
			// does not matter here — a pointer and an [figureout.OptionalOf]
			// say the same thing about the section.
			optional := f.OptionalSection()
			if child.Kind == Null && optional {
				// A section a source may leave out may also be erased, which
				// drops the section rather than emptying it.
				if !b.AllowNull {
					b.errorf(layer, path, child.Pos, figureout.CodeSourceUnsupported,
						"%s does not represent null", b.Source)
					continue
				}
				layer.SetNull(path, b.origin(docPath, pos))
				continue
			}
			if child.Kind != Object {
				// A ScalarOr field accepts its scalar spelling here; the core
				// widens it into the object.
				if short, ok := f.Shorthand(); ok {
					b.shorthand(layer, f, short, child, path, docPath, pos)
					continue
				}
				b.errorf(layer, path, child.Pos, figureout.CodeSourceUnsupported,
					"%s must be an object, got %s", docPath, child.Kind)
				continue
			}
			if optional {
				// The section is assigned before its members, so a section
				// whose every member defaults is still a section rather than a
				// nil pointer.
				layer.Set(path, figureout.Section{}, b.origin(docPath, pos))
			}
			b.object(layer, f.Type.Object, child, path+".", docPath+".", nil)
		case collection && f.Type.Kind == figureout.TypeList:
			b.elements(layer, f, elem, child, path, docPath)
		case collection:
			b.entries(layer, elem, child, path, docPath)
		default:
			b.leaf(layer, f, child, path, docPath, pos)
		}
	}

	if b.DisallowUnknown {
		for _, key := range node.Keys() {
			if _, ok := claimed[key]; ok {
				continue
			}
			// A root schema reference is an editor annotation, not configuration:
			// it names the schema describing the file, so no descriptor declares
			// it and rejecting it would break the schema we generate.
			if prefix == "" && key == SchemaKey {
				continue
			}
			_, pos, _ := node.Field(key)
			b.errorf(layer, prefix+key, pos, figureout.CodeSourceUnsupported,
				"unknown configuration property %q", prefix+key)
		}
	}
}

// member finds the document member bound to a field, trying the primary name
// first and aliases after.
func (b Binder) member(node *Node, f *figureout.FieldModel) (string, *Node, Pos, bool) {
	for _, name := range b.names(f) {
		if child, pos, ok := node.Field(name); ok {
			return name, child, pos, true
		}
	}
	return "", nil, Pos{}, false
}

func (b Binder) names(f *figureout.FieldModel) []string {
	return memberNames(f, b.Source)
}

// memberNames returns the member names a source binds a field to, primary
// first and aliases after. It is nil for a field the source skips.
func memberNames(f *figureout.FieldModel, source figureout.SourceID) []string {
	primary := f.Name
	var aliases []string
	if p, ok := f.Source(source); ok {
		if p.Skip {
			return nil
		}
		if len(p.Names) > 0 {
			primary = p.Names[0]
		}
		for _, o := range p.Options {
			if a, ok := o.(AliasOption); ok {
				aliases = append(aliases, a.Names...)
			}
		}
	}
	return append([]string{primary}, aliases...)
}

// union reads the discriminator and binds the matching variant against the
// same node, so a variant's members are siblings of the tag.
func (b Binder) union(layer *figureout.Layer, f *figureout.FieldModel, node *Node, base, docPath string) {
	if node.Kind != Object {
		b.errorf(layer, base, node.Pos, figureout.CodeUnionInvalid,
			"%s must be an object, got %s", docPath, node.Kind)
		return
	}

	u := f.Type.Union
	path := base + "." + u.Discriminator
	tagNode, tagPos, ok := node.Field(u.Discriminator)
	if !ok {
		b.errorf(layer, path, node.Pos, figureout.CodeMissingDefinition,
			"%s requires discriminator %q", docPath, u.Discriminator)
		return
	}

	tag, err := b.Decoder.DecodeScalar(figureout.Type{Kind: figureout.TypeString, Go: stringType}, tagNode, nil)
	if err != nil {
		b.errorf(layer, path, tagNode.Pos, figureout.CodeUnionInvalid, "%s", err)
		return
	}
	layer.Set(path, tag, b.origin(docPath+"."+u.Discriminator, tagPos))

	for _, variant := range u.Variants {
		if variant.Tag != tag {
			continue
		}
		// The tag is a sibling of the variant's members, so it is already
		// accounted for when the variant's object is bound to the same node.
		b.object(layer, variant.Object, node, base+".", docPath+".", map[string]struct{}{
			u.Discriminator: {},
		})
		return
	}

	tags := make([]string, len(u.Variants))
	for i, variant := range u.Variants {
		tags[i] = variant.Tag
	}
	b.errorf(layer, path, tagNode.Pos, figureout.CodeUnionInvalid,
		"unknown variant %q, want one of [%s]", tag, strings.Join(tags, ", "))
}

// elements binds a list whose items are objects, giving each item a path of its
// own so it merges, validates and reports like any other part of the document.
func (b Binder) elements(
	layer *figureout.Layer,
	f *figureout.FieldModel,
	elem *figureout.ObjectModel,
	node *Node,
	path, docPath string,
) {
	if node.Kind == Null {
		// Null erases a collection as it erases anything else, rather than
		// being a shape it could have been written in.
		if !b.AllowNull {
			b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(path, b.origin(docPath, node.Pos))
		return
	}
	if node.Kind != Array {
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
			"%s must be an array, got %s", docPath, node.Kind)
		return
	}
	// The list itself is assigned, so the merge policy can tell "this layer
	// provided the list" from "this layer said nothing about it".
	layer.Set(path, figureout.Collection{}, b.origin(docPath, node.Pos))

	seen := map[string]int{}
	for i, item := range node.Items {
		where := fmt.Sprintf("%s[%d]", docPath, i)
		if item.Kind != Object {
			b.errorf(layer, path, item.Pos, figureout.CodeSourceUnsupported,
				"%s must be an object, got %s", where, item.Kind)
			continue
		}

		key, ok := b.elementKey(layer, f, item, path, where, i)
		if !ok {
			continue
		}
		if prev, dup := seen[key]; dup {
			b.errorf(layer, figureout.ElementPath(path, key), item.Pos, figureout.CodeDuplicateName,
				"%s repeats %s, which identifies an element", where, fmt.Sprintf("%s[%d]", docPath, prev))
			continue
		}
		seen[key] = i

		elemPath := figureout.ElementPath(path, key)
		// The element is assigned before its members, so an element whose every member is
		// absent still claims a slot rather than vanishing from the list.
		layer.Set(elemPath, figureout.Element{}, b.origin(where, item.Pos))
		b.object(layer, elem, item, elemPath+".", where+".", nil)
	}
}

// elementKey returns the subscript identifying one element: its merge key when
// the list declares one, and its position otherwise.
//
// A key has to be read before the rest of the element, the same way a union's
// discriminator does, because it decides where the element's members land.
func (b Binder) elementKey(
	layer *figureout.Layer,
	f *figureout.FieldModel,
	item *Node,
	path, where string,
	index int,
) (string, bool) {
	key, ok := f.MergeKey()
	if !ok {
		return strconv.Itoa(index), true
	}

	node, _, found := item.Field(key.Name)
	if !found || node.Kind != Scalar {
		b.errorf(layer, path, item.Pos, figureout.CodeMissingDefinition,
			"%s must set %q, which identifies an element of %s", where, key.Name, f.Name)
		return "", false
	}
	return key.Name + "=" + fmt.Sprint(Raw(node)), true
}

// entries binds a map whose values are objects. A map already has an identity
// for each entry, so its key is the subscript.
func (b Binder) entries(
	layer *figureout.Layer,
	elem *figureout.ObjectModel,
	node *Node,
	path, docPath string,
) {
	if node.Kind == Null {
		// Null erases a collection as it erases anything else, rather than
		// being a shape it could have been written in.
		if !b.AllowNull {
			b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(path, b.origin(docPath, node.Pos))
		return
	}
	if node.Kind != Object {
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
			"%s must be an object, got %s", docPath, node.Kind)
		return
	}
	layer.Set(path, figureout.Collection{}, b.origin(docPath, node.Pos))

	for _, entry := range node.Fields {
		where := docPath + "." + entry.Key
		entryPath := figureout.ElementPath(path, entry.Key)

		if entry.Value.Kind == Null {
			// Removing one entry is spellable here, and null already means
			// erase, so it needs no directive of its own.
			if !b.AllowNull {
				b.errorf(layer, entryPath, entry.Value.Pos, figureout.CodeSourceUnsupported,
					"%s does not represent null", b.Source)
				continue
			}
			layer.SetNull(entryPath, b.origin(where, entry.Pos))
			continue
		}
		if entry.Value.Kind != Object {
			b.errorf(layer, entryPath, entry.Value.Pos, figureout.CodeSourceUnsupported,
				"%s must be an object, got %s", where, entry.Value.Kind)
			continue
		}
		layer.Set(entryPath, figureout.Element{}, b.origin(where, entry.Pos))
		b.object(layer, elem, entry.Value, entryPath+".", where+".", nil)
	}
}

// decoderOf returns the decoder a field installed for this source.
func decoderOf(f *figureout.FieldModel, id figureout.SourceID) (figureout.Decoder, bool) {
	p, ok := f.Source(id)
	if !ok || p.Skip || p.Decoder == nil {
		return nil, false
	}
	return p.Decoder, true
}

// decode hands a node to the field's own decoder, gated on the shapes the field
// declared. A shape it did not declare fails the same way it would without a
// decoder, so declaring shapes stays the thing that decides what is accepted.
func (b Binder) decode(
	layer *figureout.Layer,
	f *figureout.FieldModel,
	dec figureout.Decoder,
	node *Node,
	path string,
	docPath string,
	pos Pos,
) {
	origin := b.origin(docPath, pos)

	// Null stays a merge directive: it erases rather than reaching a decoder.
	if node.Kind == Null {
		if !b.AllowNull {
			b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(path, origin)
		return
	}

	accepts := acceptsOf(f, b.Source)
	if !accepted(accepts, node) {
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
			"want %s, got %s", shapeNames(accepts), node.Kind)
		return
	}

	v, err := dec.DecodeValue(Raw(node))
	if err != nil {
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported, "%s",
			figureout.Redact(f, err.Error(), node.Text, node.Value))
		return
	}
	layer.Set(path, v, origin)
}

// accepted reports whether a node matches one of the declared shapes.
func accepted(accepts []figureout.Shape, node *Node) bool {
	if len(accepts) == 0 {
		return true
	}
	want := ShapeOf(node)
	for _, s := range accepts {
		// A scalar node reports an unknown shape: which scalar it is belongs to
		// the format, so any scalar shape accepts it.
		if s.Kind == want || (want == figureout.ShapeUnknown && scalarShape(s.Kind)) {
			return true
		}
	}
	return false
}

func scalarShape(k figureout.ShapeKind) bool {
	switch k {
	case figureout.ShapeBoolean, figureout.ShapeInteger, figureout.ShapeNumber, figureout.ShapeString:
		return true
	default:
		return false
	}
}

func shapeNames(accepts []figureout.Shape) string {
	names := make([]string, 0, len(accepts))
	for _, s := range accepts {
		names = append(names, s.Kind.String())
	}
	return strings.Join(names, " or ")
}

// shorthand binds the scalar spelling of an object field.
func (b Binder) shorthand(
	layer *figureout.Layer,
	f *figureout.FieldModel,
	short figureout.Type,
	node *Node,
	path string,
	docPath string,
	pos Pos,
) {
	origin := b.origin(docPath, pos)
	if node.Kind == Null {
		if !b.AllowNull {
			b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(path, origin)
		return
	}

	v, err := b.value(short, node, acceptsOf(f, b.Source))
	if err != nil {
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported, "%s",
			figureout.Redact(f, err.Error(), node.Text, node.Value))
		return
	}
	layer.Set(path, v, origin)
}

// opaque binds a subtree verbatim, whatever it holds.
//
// Nothing here is checked against a shape: the descriptor does not describe
// what is inside, so there is nothing to check it against, and the field's own
// Go type is what finally decides whether the value fits.
func (b Binder) opaque(
	layer *figureout.Layer,
	f *figureout.FieldModel,
	node *Node,
	path, docPath string,
	pos Pos,
) {
	origin := b.origin(docPath, pos)
	if node.Kind == Null {
		// Null stays a merge directive even here: it erases the block rather
		// than passing a nil through as its contents.
		if !b.AllowNull {
			b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(path, origin)
		return
	}

	v, err := b.Decoder.DecodeAny(node)
	if err != nil {
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported, "%s",
			figureout.Redact(f, err.Error(), node.Text, node.Value))
		return
	}
	layer.Set(path, v, origin)
}

func (b Binder) leaf(layer *figureout.Layer, f *figureout.FieldModel, node *Node, path, docPath string, pos Pos) {
	origin := b.origin(docPath, pos)

	if node.Kind == Null {
		if !b.AllowNull {
			b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(path, origin)
		return
	}

	v, err := b.value(f.Type, node, acceptsOf(f, b.Source))
	if err != nil {
		// A decoding failure quotes what it could not read, which for a secret
		// field is the secret itself.
		b.errorf(layer, path, node.Pos, figureout.CodeSourceUnsupported, "%s",
			figureout.Redact(f, err.Error(), node.Text, node.Value))
		return
	}
	layer.Set(path, v, origin)
}

// value converts a node to the semantic Go value of t. Structure is shared;
// scalars go to the format's decoder.
func (b Binder) value(t figureout.Type, node *Node, accepts []figureout.Shape) (any, error) {
	switch t.Kind {
	case figureout.TypeList:
		return b.list(t, node)
	case figureout.TypeMap:
		return b.mapping(t, node)
	case figureout.TypeObject, figureout.TypeUnion:
		return nil, errors.Errorf("cannot decode a %s here", t.Kind)
	default:
		if node.Kind != Scalar {
			return nil, errors.Errorf("want a %s, got %s", t.Kind, node.Kind)
		}
		return b.Decoder.DecodeScalar(t, node, accepts)
	}
}

func (b Binder) list(t figureout.Type, node *Node) (any, error) {
	if node.Kind != Array {
		return nil, errors.Errorf("want an array, got %s", node.Kind)
	}
	if t.Elem == nil || t.Go.Kind() != reflect.Slice {
		return nil, errors.Errorf("cannot decode an array into %s", t.Go)
	}

	out := reflect.MakeSlice(t.Go, 0, len(node.Items))
	for i, item := range node.Items {
		v, err := b.value(*t.Elem, item, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "element %d", i)
		}
		out = reflect.Append(out, reflect.ValueOf(v))
	}
	return out.Interface(), nil
}

func (b Binder) mapping(t figureout.Type, node *Node) (any, error) {
	if node.Kind != Object {
		return nil, errors.Errorf("want an object, got %s", node.Kind)
	}
	if t.Key == nil || t.Elem == nil || t.Go.Kind() != reflect.Map {
		return nil, errors.Errorf("cannot decode an object into %s", t.Go)
	}

	out := reflect.MakeMapWithSize(t.Go, len(node.Fields))
	for _, f := range node.Fields {
		key, err := scalar.ParseText(*t.Key, f.Key, "")
		if err != nil {
			return nil, errors.Wrapf(err, "key %q", f.Key)
		}
		v, err := b.value(*t.Elem, f.Value, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "key %q", f.Key)
		}
		out.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(v))
	}
	return out.Interface(), nil
}

func (b Binder) origin(docPath string, pos Pos) figureout.Origin {
	return figureout.Origin{
		Source: b.Source,
		Name:   docPath,
		File:   b.File,
		Line:   pos.Line,
		Col:    pos.Col,
	}
}

func (b Binder) errorf(layer *figureout.Layer, path string, pos Pos, code, format string, args ...any) {
	origin := b.origin(path, pos)
	layer.Diagnostics = append(layer.Diagnostics, figureout.Diagnostic{
		Severity:  figureout.SeverityError,
		Code:      code,
		Message:   errors.Errorf(format, args...).Error(),
		FieldPath: path,
		Source:    b.Source,
		Origin:    &origin,
	})
}

// acceptsOf returns the wire shapes the field declared for this source.
func acceptsOf(f *figureout.FieldModel, id figureout.SourceID) []figureout.Shape {
	p, ok := f.Source(id)
	if !ok {
		return nil
	}
	return p.Accepts
}

var stringType = reflect.TypeFor[string]()
