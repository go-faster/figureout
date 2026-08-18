package figureout_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type cacheSection struct {
	Dir   string
	Bytes int
}

// carriedConfig holds the same section under both carriers, so every case is
// stated twice and the two spellings are held to the same answer.
type carriedConfig struct {
	Backend string
	Inline  figureout.OptionalOf[cacheSection]
	Boxed   figureout.OptionalOf[*cacheSection]
}

func describeCache(c *cacheSection, s *figureout.Schema[cacheSection]) {
	figureout.Explicit(s, &c.Dir, "dir")
	figureout.Value(s, &c.Bytes, "bytes").ApplyDefault(1024)
}

func describeCarried(c *carriedConfig, s *figureout.Schema[carriedConfig]) {
	figureout.Value(s, &c.Backend, "backend")
	figureout.OptionalObjectFunc(s, &c.Inline, "inline", describeCache)
	figureout.OptionalObjectFunc(s, &c.Boxed, "boxed", describeCache)
}

// A carrier says whether the section is there. That is the whole question, and
// "OptionalOf[*C]" answers it in the carrier too — the pointer is an ordinary
// required one, allocated whenever the section is present.
func TestOptionalSectionAbsentPresentEmpty(t *testing.T) {
	d, err := figureout.Derive(describeCarried)
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("inline")
	require.True(t, ok)
	require.Equal(t, figureout.PresenceOptional, f.Presence)
	require.False(t, f.Required())

	f, ok = d.Model().FieldByPath("boxed")
	require.True(t, ok)
	require.Equal(t, figureout.PresenceOptional, f.Presence, "the pointer inside is not presence")

	t.Run("Absent", func(t *testing.T) {
		cfg, _, err := d.Resolve(yaml.Bytes([]byte("backend: file\n")))
		require.NoError(t, err)
		require.False(t, cfg.Inline.IsSet())
		require.False(t, cfg.Boxed.IsSet())
	})

	t.Run("Present", func(t *testing.T) {
		cfg, _, err := d.Resolve(yaml.Bytes([]byte(
			"inline:\n  dir: /a\n  bytes: 7\nboxed:\n  dir: /b\n  bytes: 9\n")))
		require.NoError(t, err)

		inline, ok := cfg.Inline.Value()
		require.True(t, ok)
		require.Equal(t, cacheSection{Dir: "/a", Bytes: 7}, inline)

		boxed, ok := cfg.Boxed.Value()
		require.True(t, ok)
		require.NotNil(t, boxed, "a present section allocates the pointer it is held behind")
		require.Equal(t, cacheSection{Dir: "/b", Bytes: 9}, *boxed)
	})

	// A section that is there and defaulted throughout still has to be there:
	// this is what a zero struct cannot say and the carrier can.
	t.Run("EmptyIsNotAbsent", func(t *testing.T) {
		_, _, err := d.Resolve(yaml.Bytes([]byte("inline: {}\n")))
		require.Error(t, err, "dir is required inside a section that is present")
		require.Contains(t, err.Error(), "inline.dir")
	})
}

// Explicit inside an optional section means "required where the section is",
// not "required everywhere" — the members are never materialized otherwise.
func TestExplicitOnlyWhereTheSectionIs(t *testing.T) {
	d, err := figureout.Derive(describeCarried)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("backend: file\n")))
	require.NoError(t, err, "a section nobody wrote demands nothing")

	_, _, err = d.Resolve(yaml.Bytes([]byte("boxed:\n  bytes: 4\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "boxed.dir")
}

// Erasing a section erases what is in it: the members of a section that is gone
// are not a section that is half there.
func TestOptionalSectionErased(t *testing.T) {
	d, err := figureout.Derive(describeCarried)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(
		yaml.Bytes([]byte("inline:\n  dir: /a\nboxed:\n  dir: /b\n")),
		yaml.Bytes([]byte("inline: null\nboxed: null\n")),
	)
	require.NoError(t, err)
	require.False(t, cfg.Inline.IsSet())
	require.False(t, cfg.Boxed.IsSet())
}

// A flat source has no name for the section itself, so a member it provided is
// the whole of what it can say — and that has to be enough.
func TestOptionalSectionFromFlatSource(t *testing.T) {
	d, err := figureout.Derive(describeCarried)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"BOXED_DIR": "/from-env"}))
	require.NoError(t, err)

	boxed, ok := cfg.Boxed.Value()
	require.True(t, ok)
	require.NotNil(t, boxed)
	require.Equal(t, "/from-env", boxed.Dir)
	require.Equal(t, 1024, boxed.Bytes, "a default applies inside a section that is present")

	require.False(t, cfg.Inline.IsSet(), "a section nothing was written under stays absent")
}

// Descriptor.Value reaches through a carrier, and reports false rather than
// reading through a section that is not there.
func TestOptionalSectionValueLookup(t *testing.T) {
	d, err := figureout.Derive(describeCarried)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("boxed:\n  dir: /b\n")))
	require.NoError(t, err)

	v, ok := d.Value(&cfg, "boxed.dir")
	require.True(t, ok)
	require.Equal(t, "/b", v)

	_, ok = d.Value(&cfg, "inline.dir")
	require.False(t, ok, "a path into an unset section holds no value")
}

// An invariant registered on a nested schema runs where the section is and
// nowhere else.
type invariantConfig struct {
	Cache figureout.OptionalOf[*cacheSection]
}

func describeInvariant(c *invariantConfig, s *figureout.Schema[invariantConfig]) {
	figureout.OptionalObjectFunc(s, &c.Cache, "cache", func(c *cacheSection, s *figureout.Schema[cacheSection]) {
		figureout.Explicit(s, &c.Dir, "dir")
		figureout.Value(s, &c.Bytes, "bytes")
		figureout.Invariant(s, "bytes-positive", func(c *cacheSection) error {
			if c.Bytes <= 0 {
				return figureout.At("bytes").Errorf("must be positive, got %d", c.Bytes)
			}
			return nil
		})
	})
}

func TestInvariantInsideOptionalSection(t *testing.T) {
	d, err := figureout.Derive(describeInvariant)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("{}\n")))
	require.NoError(t, err, "a rule about a section nobody wrote has nothing to be violated by")

	_, _, err = d.Resolve(yaml.Bytes([]byte("cache:\n  dir: /a\n  bytes: 0\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cache.bytes")

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("cache:\n  dir: /a\n  bytes: 8\n")))
	require.NoError(t, err)
	cache, ok := cfg.Cache.Value()
	require.True(t, ok)
	require.Equal(t, 8, cache.Bytes)
}

// Absence is spelled once. A carrier behind a pointer and a carrier inside a
// carrier are both two answers to one question.
func TestStackedCarriersRejected(t *testing.T) {
	type doubled struct {
		A *figureout.OptionalOf[int]
		B figureout.OptionalOf[figureout.OptionalOf[int]]
	}

	_, err := figureout.Derive(func(c *doubled, s *figureout.Schema[doubled]) {
		figureout.Value(s, &c.A, "a")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absence has to be spelled once")

	_, err = figureout.Derive(func(c *doubled, s *figureout.Schema[doubled]) {
		figureout.Optional(s, &c.B, "b")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absence has to be spelled once")
}

// An optional registrar on a section that is always there names the one to use
// instead, rather than quietly making it optional.
//
// The other direction — [figureout.ObjectFunc] on a carrier — does not reach a
// diagnostic at all: the required registrars infer the object type from the
// field, so a carrier there is a type error at the call site, which is the
// earlier and better answer.
func TestSectionRegistrarMismatch(t *testing.T) {
	type mismatched struct {
		Cache cacheSection
	}

	_, err := figureout.Derive(func(c *mismatched, s *figureout.Schema[mismatched]) {
		figureout.OptionalObjectFunc(s, &c.Cache, "cache", describeCache)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "carries required presence")
	require.Contains(t, err.Error(), "Object or ObjectFunc")
}

// Both carriers reach the targets as the same optional object: a section a
// source may leave out is not in "required", and a section that is there still
// has its own required members.
func TestOptionalSectionSchema(t *testing.T) {
	d, err := figureout.Derive(describeCarried)
	require.NoError(t, err)

	data, diags, err := jsonschema.Generate(d)
	require.NoError(t, err)
	require.Empty(t, diags)

	var doc struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type     string   `json:"type"`
			Required []string `json:"required"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	require.NotContains(t, doc.Required, "inline")
	require.NotContains(t, doc.Required, "boxed")

	for _, name := range []string{"inline", "boxed"} {
		prop, ok := doc.Properties[name]
		require.True(t, ok, name)
		require.Equal(t, "object", prop.Type, name)
		require.Equal(t, []string{"dir"}, prop.Required, name)
	}
}
