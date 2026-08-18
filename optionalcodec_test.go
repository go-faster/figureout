package figureout_test

import (
	"encoding/json"
	"testing"

	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
)

// A converted struct has to keep marshaling the way its consumers expect: the
// carrier is invisible in the document, and missing is null.
func TestOptionalOfRoundTrips(t *testing.T) {
	type doc struct {
		Name  string                              `json:"name" yaml:"name"`
		Count figureout.OptionalOf[int]           `json:"count" yaml:"count"`
		Inner figureout.OptionalOf[*cacheSection] `json:"inner" yaml:"inner"`
	}

	t.Run("JSON", func(t *testing.T) {
		var in doc
		in.Name = "a"
		in.Count = figureout.Some(3)

		data, err := json.Marshal(in)
		require.NoError(t, err)
		require.JSONEq(t, `{"name":"a","count":3,"inner":null}`, string(data))

		var out doc
		require.NoError(t, json.Unmarshal(data, &out))
		require.Equal(t, in, out)

		require.NoError(t, json.Unmarshal([]byte(`{"count":null}`), &out))
		require.False(t, out.Count.IsSet(), "null decodes as missing, not as a present zero")
	})

	t.Run("YAML", func(t *testing.T) {
		var in doc
		in.Name = "a"
		in.Inner = figureout.Some(&cacheSection{Dir: "/d", Bytes: 2})

		data, err := yaml.Marshal(in)
		require.NoError(t, err)
		require.Contains(t, string(data), "dir: /d", "the carrier is invisible in the document")

		var out doc
		require.NoError(t, yaml.Unmarshal(data, &out))
		require.False(t, out.Count.IsSet())

		inner, ok := out.Inner.Value()
		require.True(t, ok)
		require.Equal(t, cacheSection{Dir: "/d", Bytes: 2}, *inner)
	})
}
