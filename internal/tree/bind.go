package tree

import (
	"reflect"
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
	b.object(layer, m.Root, root, "", nil)
	return layer
}

// object binds an object node. claimed is seeded with member names already
// consumed by the caller, such as a union's discriminator.
func (b Binder) object(
	layer *figureout.Layer,
	obj *figureout.ObjectModel,
	node *Node,
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

		switch {
		case f.Type.Union != nil:
			b.union(layer, f, child, docPath)
		case f.Type.Object != nil:
			if child.Kind != Object {
				// A ScalarOr field accepts its scalar spelling here; the core
				// widens it into the object.
				if short, ok := f.Shorthand(); ok {
					b.shorthand(layer, f, short, child, docPath, pos)
					continue
				}
				b.errorf(layer, f.Path, child.Pos, figureout.CodeSourceUnsupported,
					"%s must be an object, got %s", docPath, child.Kind)
				continue
			}
			b.object(layer, f.Type.Object, child, docPath+".", nil)
		default:
			b.leaf(layer, f, child, docPath, pos)
		}
	}

	if b.DisallowUnknown {
		for _, key := range node.Keys() {
			if _, ok := claimed[key]; ok {
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
	primary := f.Name
	var aliases []string
	if p, ok := f.Source(b.Source); ok {
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
func (b Binder) union(layer *figureout.Layer, f *figureout.FieldModel, node *Node, docPath string) {
	if node.Kind != Object {
		b.errorf(layer, f.Path, node.Pos, figureout.CodeUnionInvalid,
			"%s must be an object, got %s", docPath, node.Kind)
		return
	}

	u := f.Type.Union
	path, _ := figureout.DiscriminatorPath(f)
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
		b.object(layer, variant.Object, node, docPath+".", map[string]struct{}{
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

// shorthand binds the scalar spelling of an object field.
func (b Binder) shorthand(
	layer *figureout.Layer,
	f *figureout.FieldModel,
	short figureout.Type,
	node *Node,
	docPath string,
	pos Pos,
) {
	origin := b.origin(docPath, pos)
	if node.Kind == Null {
		if !b.AllowNull {
			b.errorf(layer, f.Path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(f.Path, origin)
		return
	}

	v, err := b.value(short, node, acceptsOf(f, b.Source))
	if err != nil {
		b.errorf(layer, f.Path, node.Pos, figureout.CodeSourceUnsupported, "%s",
			figureout.Redact(f, err.Error(), node.Text, node.Value))
		return
	}
	layer.Set(f.Path, v, origin)
}

func (b Binder) leaf(layer *figureout.Layer, f *figureout.FieldModel, node *Node, docPath string, pos Pos) {
	origin := b.origin(docPath, pos)

	if node.Kind == Null {
		if !b.AllowNull {
			b.errorf(layer, f.Path, node.Pos, figureout.CodeSourceUnsupported,
				"%s does not represent null", b.Source)
			return
		}
		layer.SetNull(f.Path, origin)
		return
	}

	v, err := b.value(f.Type, node, acceptsOf(f, b.Source))
	if err != nil {
		// A decoding failure quotes what it could not read, which for a secret
		// field is the secret itself.
		b.errorf(layer, f.Path, node.Pos, figureout.CodeSourceUnsupported, "%s",
			figureout.Redact(f, err.Error(), node.Text, node.Value))
		return
	}
	layer.Set(f.Path, v, origin)
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
