package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type patternSite struct {
	Name     string
	Patterns []string
}

type patternConfig struct {
	Sites []patternSite
}

// TestExplicitCollectionIsRequired covers a collection registered with
// Explicit: the absent-is-empty rule is what a collection does when nobody says
// otherwise, and Explicit says otherwise.
func TestExplicitCollectionIsRequired(t *testing.T) {
	type Cfg struct {
		Tags []string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Explicit(s, &c.Tags, "tags").MinItems(1)
	})
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("tags")
	require.True(t, ok)
	require.True(t, f.Required())

	_, _, err = d.Resolve(env.Values(nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value provided and no default")

	cfg, _, err := d.Resolve(env.Values(map[string]string{"TAGS": "a,b"}))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, cfg.Tags)
}

// TestExplicitCollectionInsideListElement covers the same rule one level down,
// where a missing key belongs to an element rather than to the root.
func TestExplicitCollectionInsideListElement(t *testing.T) {
	d, err := figureout.Derive(func(c *patternConfig, s *figureout.Schema[patternConfig]) {
		figureout.ListOf(s, &c.Sites, "sites", func(e *patternSite, s *figureout.Schema[patternSite]) {
			figureout.Explicit(s, &e.Name, "name").NonEmpty()
			figureout.Explicit(s, &e.Patterns, "patterns").MinItems(1)
		}).MergeByKey("name")
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("sites:\n  - name: docs\n")))
	require.Error(t, err, "a site with no patterns matches nothing")
	require.Contains(t, err.Error(), "sites[name=docs].patterns")
	require.Contains(t, err.Error(), "no value provided and no default")

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("sites:\n  - name: docs\n    patterns: [\"*.md\"]\n")))
	require.NoError(t, err)
	require.Equal(t, []string{"*.md"}, cfg.Sites[0].Patterns)
}

func TestValueCollectionRequiredOptsBackIn(t *testing.T) {
	type Cfg struct {
		Tags []string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Tags, "tags").Required()
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(env.Values(nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value provided and no default")
}

func TestExplicitCollectionWithDefaultIsNotRequired(t *testing.T) {
	type Cfg struct {
		Tags []string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Explicit(s, &c.Tags, "tags").ApplyDefault([]string{"a"})
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(nil))
	require.NoError(t, err, "a default is a value, so nothing is missing")
	require.Equal(t, []string{"a"}, cfg.Tags)
}

// TestEmptyCollectionRejectedByItsOwnConstraints covers the other half: a
// collection that falls back to empty cannot be constrained to reject empty,
// because absence would resolve to a value the field itself refuses.
func TestEmptyCollectionRejectedByItsOwnConstraints(t *testing.T) {
	type Cfg struct {
		Tags []string
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Tags, "tags").MinItems(1)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "an empty list does not satisfy")
	require.Contains(t, err.Error(), "mark it Required")
}

func TestEmptyMapRejectedByItsOwnConstraints(t *testing.T) {
	type Cfg struct {
		Limits map[string]int
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Limits, "limits").MinItems(1)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "an empty map does not satisfy")
}
