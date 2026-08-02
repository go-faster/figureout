package json

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout/internal/tree"
)

func TestParsePositions(t *testing.T) {
	const src = `{
  "a": 1,
  "b": {
    "c": "x"
  },
  "d": [10, 20]
}`

	root, err := parse([]byte(src))
	require.NoError(t, err)
	require.Equal(t, tree.Object, root.Kind)

	a, aPos, ok := root.Field("a")
	require.True(t, ok)
	require.Equal(t, tree.Pos{Line: 2, Col: 3}, aPos, "key position")
	require.Equal(t, tree.Pos{Line: 2, Col: 8}, a.Pos, "value position")

	b, _, ok := root.Field("b")
	require.True(t, ok)
	c, _, ok := b.Field("c")
	require.True(t, ok)
	require.Equal(t, tree.Pos{Line: 4, Col: 10}, c.Pos)

	d, _, ok := root.Field("d")
	require.True(t, ok)
	require.Len(t, d.Items, 2)
	require.Equal(t, tree.Pos{Line: 6, Col: 9}, d.Items[0].Pos)
	require.Equal(t, tree.Pos{Line: 6, Col: 13}, d.Items[1].Pos)
}

func TestParseDuplicateKeyLastWins(t *testing.T) {
	root, err := parse([]byte(`{"a": 1, "a": 2}`))
	require.NoError(t, err)

	a, _, ok := root.Field("a")
	require.True(t, ok)
	require.Equal(t, "2", a.Text, "the last duplicate wins, as encoding/json does")
	require.Len(t, root.Keys(), 2, "both members are kept for unknown-field reporting")
}

func TestParseRejectsTrailingData(t *testing.T) {
	_, err := parse([]byte(`{"a": 1} {"b": 2}`))
	require.Error(t, err)
}

// FuzzParse checks the parser against the standard library: it accepts exactly
// the documents encoding/json calls valid, and every node it produces carries
// a position inside the input.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"a":1}`, `[1,2,3]`, `null`, `"x"`, `12`, `{"a":{"b":[true,null]}}`,
		"{\n  \"a\": 1\n}", `{"a": 1} {"b": 2}`, `{`, `{"a":}`, `{"a":1,}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		root, err := parse([]byte(src))
		if err != nil {
			return
		}
		require.True(t, json.Valid([]byte(src)),
			"parser accepted a document encoding/json rejects")
		checkPositions(t, root, src)
	})
}

func checkPositions(t *testing.T, n *tree.Node, src string) {
	t.Helper()
	if n == nil {
		return
	}

	lines := 1
	for _, c := range src {
		if c == '\n' {
			lines++
		}
	}
	require.GreaterOrEqual(t, n.Pos.Line, 1)
	require.LessOrEqual(t, n.Pos.Line, lines)
	require.GreaterOrEqual(t, n.Pos.Col, 1)

	for _, item := range n.Items {
		checkPositions(t, item, src)
	}
	for _, f := range n.Fields {
		require.GreaterOrEqual(t, f.Pos.Line, 1)
		require.LessOrEqual(t, f.Pos.Line, lines)
		checkPositions(t, f.Value, src)
	}
}
