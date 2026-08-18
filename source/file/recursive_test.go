package file_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/file"
)

type node struct {
	Name  string
	Child figureout.OptionalOf[*node]
}

func describeNode(c *node, s *figureout.Schema[node]) {
	figureout.Value(s, &c.Name, "name")
	figureout.OptionalObjectFunc(s, &c.Child, "child", describeNode)
}

// One file holds one value, and a recursive shape has unboundedly many paths to
// hold. The cycle is where this source stops, the way a collection of objects
// already is.
func TestRecursiveStopsAtTheCycle(t *testing.T) {
	d, err := figureout.Derive(describeNode)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(file.FS(fstest.MapFS{
		"name": {Data: []byte("root")},
		// No file names a level of the recursion, so none is read.
		"child.name": {Data: []byte("ignored")},
	}))
	require.NoError(t, err)
	require.Equal(t, "root", cfg.Name)
	require.False(t, cfg.Child.IsSet())
}
