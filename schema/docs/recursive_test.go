package docs_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/docs"
	"github.com/go-faster/figureout/source/yaml"
)

type Node struct {
	Name  string
	Child figureout.OptionalOf[*Node]
}

func describeNode(c *Node, s *figureout.Schema[Node]) {
	figureout.Value(s, &c.Name, "name")
	figureout.OptionalObjectFunc(s, &c.Child, "child", describeNode)
}

// A recursive shape has infinitely many paths, so the page documents the object
// once and the field that re-enters it links back to that section.
func TestRecursiveDocumentedOnce(t *testing.T) {
	d, err := figureout.Derive(describeNode)
	require.NoError(t, err)

	// A tree source names an arbitrary depth, but the model has one canonical
	// spelling per field, so it names the shallowest and stops.
	page, diags, err := docs.Build(d, docs.ForSource(yaml.Bytes(nil)))
	require.NoError(t, err)
	require.False(t, diags.HasErrors())

	require.Len(t, page.Sections, 1, "one section, not one per level")
	root := page.Sections[0]

	var child *docs.Field
	for _, f := range root.Fields {
		if f.Name == "child" {
			child = f
		}
	}
	require.NotNil(t, child)
	require.Equal(t, root.Anchor, child.Section, "the row links back to the section it re-enters")
	require.Equal(t, root.Title, child.Recursive)
	require.Equal(t, []string{"child"}, child.Names["yaml"])

	md := string(page.Markdown())
	require.Equal(t, 1, strings.Count(md, "## "), "no section repeats the shape")
	require.Contains(t, md, "Nests "+root.Title+" again.")
}
