package jsonschema_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
)

type Node struct {
	Name  string
	Child figureout.OptionalOf[*Node]
}

func describeNode(c *Node, s *figureout.Schema[Node]) {
	figureout.Value(s, &c.Name, "name")
	figureout.OptionalObjectFunc(s, &c.Child, "child", describeNode)
}

type Rule struct {
	Name string
	Sub  []Rule
}

type RuleSet struct {
	Rules []Rule
}

func describeRule(c *Rule, s *figureout.Schema[Rule]) {
	figureout.Value(s, &c.Name, "name")
	figureout.ListOf(s, &c.Sub, "sub", describeRule)
}

func describeRuleSet(c *RuleSet, s *figureout.Schema[RuleSet]) {
	figureout.ListOf(s, &c.Rules, "rules", describeRule)
}

func generate[T any](t *testing.T, describe func(*T, *figureout.Schema[T])) map[string]any {
	t.Helper()

	d, err := figureout.Derive(describe)
	require.NoError(t, err)
	data, diags, err := jsonschema.Generate(d)
	require.NoError(t, err)
	require.False(t, diags.HasErrors())

	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

// A descriptor that nests itself is emitted once. The root is the document the
// reference points into, so it refers to itself as "#".
func TestRecursiveRootRefersToTheDocument(t *testing.T) {
	doc := generate(t, describeNode)

	props := doc["properties"].(map[string]any)
	require.Equal(t, map[string]any{"$ref": "#"}, props["child"])
	require.Equal(t, map[string]any{"type": "string"}, props["name"])
	require.NotContains(t, doc, "$defs", "the root needs no definition of its own")
}

// An object below the root becomes a definition, which it refers to from
// inside itself as well as from wherever it is used.
func TestRecursiveElementBecomesADefinition(t *testing.T) {
	doc := generate(t, describeRuleSet)

	defs := doc["$defs"].(map[string]any)
	require.Len(t, defs, 1)
	rule := defs["Rule"].(map[string]any)

	ref := map[string]any{"$ref": "#/$defs/Rule"}
	sub := rule["properties"].(map[string]any)["sub"].(map[string]any)
	require.Equal(t, ref, sub["items"])

	rules := doc["properties"].(map[string]any)["rules"].(map[string]any)
	require.Equal(t, ref, rules["items"])
}
