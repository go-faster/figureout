package yaml

import (
	"strconv"

	"github.com/go-faster/errors"
	"github.com/go-faster/yaml"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/internal/scalar"
	"github.com/go-faster/figureout/internal/tree"
)

// YAML resolved tags, as returned by [yaml.Node.ShortTag].
const (
	tagNull  = "!!null"
	tagBool  = "!!bool"
	tagInt   = "!!int"
	tagFloat = "!!float"
	tagStr   = "!!str"
	tagMap   = "!!map"
	tagSeq   = "!!seq"
	tagMerge = "!!merge"
)

// convertNode turns a parsed YAML node into the shared document tree.
//
// Anchors and merge keys are resolved here, so a binder never has to know about
// aliases or about a key that is really a splice.
func convertNode(n *yaml.Node) (*tree.Node, error) {
	if n == nil || n.IsZero() {
		// An empty document decodes to a zero node rather than an error.
		return nil, nil
	}

	pos := tree.Pos{Line: n.Line, Col: n.Column}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return convertNode(n.Content[0])

	case yaml.AliasNode:
		if n.Alias == nil {
			return nil, errors.Errorf("line %d: unresolved alias %q", n.Line, n.Value)
		}
		return convertNode(n.Alias)

	case yaml.ScalarNode:
		if n.ShortTag() == tagNull {
			return &tree.Node{Kind: tree.Null, Pos: pos, Text: n.Value, Tag: tagNull}, nil
		}
		return &tree.Node{Kind: tree.Scalar, Pos: pos, Text: n.Value, Tag: n.ShortTag()}, nil

	case yaml.SequenceNode:
		out := &tree.Node{Kind: tree.Array, Pos: pos, Tag: tagSeq}
		for _, item := range n.Content {
			child, err := convertNode(item)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, child)
		}
		return out, nil

	case yaml.MappingNode:
		out := &tree.Node{Kind: tree.Object, Pos: pos, Tag: tagMap}
		var merged []tree.Field
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if key.Kind != yaml.ScalarNode {
				return nil, errors.Errorf("line %d: mapping key must be a scalar", key.Line)
			}
			if key.ShortTag() == tagMerge {
				fields, err := mergeFields(value)
				if err != nil {
					return nil, err
				}
				merged = append(merged, fields...)
				continue
			}
			child, err := convertNode(value)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, tree.Field{
				Key:   key.Value,
				Pos:   tree.Pos{Line: key.Line, Col: key.Column},
				Value: child,
			})
		}

		seen := make(map[string]struct{}, len(out.Fields))
		for _, f := range out.Fields {
			seen[f.Key] = struct{}{}
		}
		for _, f := range merged {
			if _, ok := seen[f.Key]; ok {
				continue
			}
			seen[f.Key] = struct{}{}
			out.Fields = append(out.Fields, f)
		}
		return out, nil

	default:
		return nil, errors.Errorf("line %d: unsupported node kind %v", n.Line, n.Kind)
	}
}

// mergeFields reads the mapping, or the sequence of mappings, a merge key
// refers to.
//
// The fields keep the position of the mapping they were written in, so a
// diagnostic about a merged value points at the anchor that defined it rather
// than at the "<<" that pulled it in.
func mergeFields(n *yaml.Node) ([]tree.Field, error) {
	if n.Kind == yaml.SequenceNode {
		var out []tree.Field
		for _, item := range n.Content {
			fields, err := mergeFields(item)
			if err != nil {
				return nil, err
			}
			out = append(out, fields...)
		}
		return out, nil
	}

	merged, err := convertNode(n)
	if err != nil {
		return nil, err
	}
	if merged == nil || merged.Kind != tree.Object {
		return nil, errors.Errorf("line %d: merge value must be a mapping", n.Line)
	}
	return merged.Fields, nil
}

// decoder applies YAML's scalar rules.
//
// YAML scalars are text with a resolved tag, so the shared text parser decides
// the value and the tag guards against a plain string being read as a number.
type decoder struct{}

// DecodeScalar implements [tree.ScalarDecoder].
func (decoder) DecodeScalar(t figureout.Type, n *tree.Node, _ []figureout.Shape) (any, error) {
	text := n.Text
	if err := checkTag(t.Kind, n.Tag); err != nil {
		return nil, err
	}
	switch t.Kind {
	case figureout.TypeInteger, figureout.TypeNumber:
		// YAML resolves 0x1f, 0o17, 017, 0b101 and 10_000_000 as numbers, and the shared
		// text parser reads base ten. Without canonicalizing, a spelling accepted by its
		// tag is rejected by its value — and 017 would read as seventeen rather than as
		// the fifteen YAML says it is.
		canonical, err := canonicalNumber(text)
		if err != nil {
			return nil, err
		}
		text = canonical
	default:
	}
	return scalar.ParseText(t, text, "")
}

// canonicalNumber re-spells a YAML number the way the shared text parser reads it, using YAML's
// own resolution so the two can never disagree about what a scalar is.
func canonicalNumber(text string) (string, error) {
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		return "", errors.Errorf("invalid number %q", text)
	}
	switch v := v.(type) {
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	default:
		return text, nil
	}
}

// checkTag rejects a quoted string standing in for a number or a boolean.
//
// YAML resolves "8080" to !!str and 8080 to !!int, and the distinction is the
// only thing separating a typo from a value.
func checkTag(kind figureout.TypeKind, tag string) error {
	want := ""
	switch kind {
	case figureout.TypeBoolean:
		want = tagBool
	case figureout.TypeInteger:
		want = tagInt
	case figureout.TypeNumber:
		// An integer literal is a valid float.
		if tag == tagInt {
			return nil
		}
		want = tagFloat
	case figureout.TypeString, figureout.TypeDuration, figureout.TypeTimestamp, figureout.TypeBytes:
		// Durations, timestamps and byte strings are written as scalars whose
		// resolved tag varies with their spelling, so the text decides.
		return nil
	default:
		return nil
	}

	if tag != want {
		return errors.Errorf("want %s, got %s", want, tag)
	}
	return nil
}
