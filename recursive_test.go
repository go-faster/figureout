package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

// A node has a child or it has none, so the type refers to itself while every
// value of it is a finite tree.
type treeNode struct {
	Name  string
	Child figureout.OptionalOf[*treeNode]
}

func describeTreeNode(c *treeNode, s *figureout.Schema[treeNode]) {
	figureout.Value(s, &c.Name, "name")
	figureout.OptionalObjectFunc(s, &c.Child, "child", describeTreeNode)
}

// A rule holds rules of its own, which is the same recursion through a
// collection rather than through a carrier.
type rule struct {
	Name string
	Sub  []rule
}

type ruleSet struct {
	Label string
	Rules []rule
}

func describeRule(c *rule, s *figureout.Schema[rule]) {
	figureout.Explicit(s, &c.Name, "name")
	figureout.ListOf(s, &c.Sub, "sub", describeRule)
}

func describeRuleSet(c *ruleSet, s *figureout.Schema[ruleSet]) {
	figureout.Value(s, &c.Label, "label")
	figureout.ListOf(s, &c.Rules, "rules", describeRule)
}

// A self-referential descriptor compiles: the model is a graph, and the field
// closing the cycle points back at the object that already encloses it.
func TestRecursiveModelIsAGraph(t *testing.T) {
	d, err := figureout.Derive(describeTreeNode)
	require.NoError(t, err)

	m := d.Model()
	require.Len(t, m.Fields(), 2, "the recursion contributes no fields of its own")

	child, ok := m.FieldByPath("child")
	require.True(t, ok)
	target, recursive := child.Recursive()
	require.True(t, recursive)
	require.Same(t, m.Root, target, "the child re-enters the root")

	// Every deeper spelling names the very same field, however far down it is.
	name, ok := m.FieldByPath("name")
	require.True(t, ok)
	for _, path := range []string{"child.name", "child.child.name", "child.child.child.child.name"} {
		deep, ok := m.FieldByPath(path)
		require.True(t, ok, path)
		require.Same(t, name, deep, path)
	}
	_, ok = m.FieldByPath("child.child.nope")
	require.False(t, ok)
}

// Resolution materializes exactly the levels a source provided, which is where
// the recursion actually bottoms out.
func TestRecursiveResolvesAsDeepAsWritten(t *testing.T) {
	d, err := figureout.Derive(describeTreeNode)
	require.NoError(t, err)

	cfg, rep, err := d.Resolve(yaml.Bytes([]byte(
		"name: a\n" +
			"child:\n" +
			"  name: b\n" +
			"  child:\n" +
			"    name: c\n",
	)))
	require.NoError(t, err)

	require.Equal(t, "a", cfg.Name)
	b, ok := cfg.Child.Value()
	require.True(t, ok)
	require.Equal(t, "b", b.Name)
	c, ok := b.Child.Value()
	require.True(t, ok)
	require.Equal(t, "c", c.Name)
	require.False(t, c.Child.IsSet(), "a level nobody wrote is not there")

	origin, ok := rep.OriginOf("child.child.name")
	require.True(t, ok)
	require.Equal(t, figureout.SourceID("yaml"), origin.Source)
}

func TestRecursiveCollection(t *testing.T) {
	d, err := figureout.Derive(describeRuleSet)
	require.NoError(t, err)

	cfg, rep, err := d.Resolve(yaml.Bytes([]byte(
		"label: root\n" +
			"rules:\n" +
			"  - name: outer\n" +
			"    sub:\n" +
			"      - name: inner\n" +
			"      - name: sibling\n",
	)))
	require.NoError(t, err)

	require.Equal(t, "root", cfg.Label)
	require.Len(t, cfg.Rules, 1)
	require.Equal(t, "outer", cfg.Rules[0].Name)
	require.Len(t, cfg.Rules[0].Sub, 2)
	require.Equal(t, "inner", cfg.Rules[0].Sub[0].Name)
	require.Equal(t, "sibling", cfg.Rules[0].Sub[1].Name)
	require.Empty(t, cfg.Rules[0].Sub[0].Sub)

	_, ok := rep.OriginOf("rules[0].sub[1].name")
	require.True(t, ok)
}

// Explicit is honored at every level, not only at the ones the model indexed.
func TestRecursiveExplicitInsideRecursion(t *testing.T) {
	d, err := figureout.Derive(describeRuleSet)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte(
		"rules:\n" +
			"  - name: outer\n" +
			"    sub:\n" +
			"      - sub: []\n",
	)))
	require.ErrorContains(t, err, "no value provided and no default")
}

// A later layer edits a level the earlier one wrote, rather than replacing the
// whole shape, exactly as it does outside a recursion.
func TestRecursiveMergesByLayer(t *testing.T) {
	d, err := figureout.Derive(describeTreeNode)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(
		yaml.Bytes([]byte("name: a\nchild:\n  name: b\n")),
		yaml.Bytes([]byte("child:\n  name: overridden\n")),
	)
	require.NoError(t, err)
	require.Equal(t, "a", cfg.Name)
	b, ok := cfg.Child.Value()
	require.True(t, ok)
	require.Equal(t, "overridden", b.Name)
}

// A required section that contains itself is an infinite value, so it is a
// compilation diagnostic rather than a resolution that never ends.
func TestRecursiveRequiredSectionIsRefused(t *testing.T) {
	type node struct {
		Name  string
		Child *node
	}
	var describe func(*node, *figureout.Schema[node])
	describe = func(c *node, s *figureout.Schema[node]) {
		figureout.Value(s, &c.Name, "name")
		figureout.ObjectFunc(s, &c.Child, "child", describe)
	}

	_, err := figureout.Derive(describe)
	require.ErrorContains(t, err, "re-enters")
	require.ErrorContains(t, err, "optional section or a collection")
}

// A flat source has no bounded name for an unbounded path, so it stops at the
// cycle the way it already stops at a collection of objects.
func TestRecursiveEnvStopsAtTheCycle(t *testing.T) {
	d, err := figureout.Derive(describeTreeNode)
	require.NoError(t, err)

	names := env.Values(nil).(figureout.SourceNamer).ProjectNames(d.Model())
	require.Equal(t, map[string][]string{"name": {"NAME"}}, names)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"NAME": "a"}))
	require.NoError(t, err)
	require.Equal(t, "a", cfg.Name)
	require.False(t, cfg.Child.IsSet())
}
