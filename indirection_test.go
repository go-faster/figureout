package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type s3Section struct {
	Bucket string
	Region string
}

// A pointer says only that the value is held behind one. Every field here is
// required: resolution allocates, and none of them is ever nil.
type indirectConfig struct {
	Backend string
	S3      *s3Section
}

func describeS3(c *s3Section, s *figureout.Schema[s3Section]) {
	figureout.Value(s, &c.Bucket, "bucket")
	figureout.Value(s, &c.Region, "region")
}

func describeIndirect(c *indirectConfig, s *figureout.Schema[indirectConfig]) {
	figureout.Value(s, &c.Backend, "backend")
	figureout.ObjectFunc(s, &c.S3, "s3", describeS3)
}

// A "*T" registered with Value is a required field like any other: absent
// resolves to the zero value, and the pointer to hold it is allocated. Nil is
// not one of the answers, because nil is not what a pointer means here.
func TestRequiredPointerIsNotPresence(t *testing.T) {
	d, err := figureout.Derive(describeIndirect)
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("s3")
	require.True(t, ok)
	require.Equal(t, figureout.PresenceRequired, f.Presence)
	require.False(t, f.OptionalSection(), "a pointer is indirection, not absence")

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("backend: file\n")))
	require.NoError(t, err)
	require.Equal(t, &s3Section{}, cfg.S3, "a required section is materialized, allocated")

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("s3:\n  bucket: logs\n")))
	require.NoError(t, err)
	require.Equal(t, &s3Section{Bucket: "logs"}, cfg.S3)
}

// The pointer resolution writes is fresh, so a resolved configuration aliases
// nothing a source is still holding.
func TestRequiredPointerDoesNotAlias(t *testing.T) {
	d, err := figureout.Derive(describeIndirect)
	require.NoError(t, err)

	a, _, err := d.Resolve(yaml.Bytes([]byte("s3:\n  bucket: logs\n")))
	require.NoError(t, err)
	b, _, err := d.Resolve(yaml.Bytes([]byte("s3:\n  bucket: logs\n")))
	require.NoError(t, err)

	require.Equal(t, a.S3, b.S3)
	require.NotSame(t, a.S3, b.S3)
}

// A pointer to a scalar is refused. It is not presence — that is the carrier's
// job — and a scalar has no identity or size a pointer would preserve, so all
// it would do is make this builder's constraints and default pointer-typed.
func TestPointerToScalarRefused(t *testing.T) {
	type cfg struct {
		Retries *int
	}
	_, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.Value(s, &c.Retries, "retries")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pointer to a scalar")
	require.Contains(t, err.Error(), "OptionalOf[int]")
}

// Explicit inside a section held behind a pointer is demanded like any other
// member: the pointer is not what says whether the section is there.
func TestExplicitInsidePointerSection(t *testing.T) {
	d, err := figureout.Derive(func(c *indirectConfig, s *figureout.Schema[indirectConfig]) {
		figureout.Value(s, &c.Backend, "backend")
		figureout.ObjectFunc(s, &c.S3, "s3", func(c *s3Section, s *figureout.Schema[s3Section]) {
			figureout.Explicit(s, &c.Bucket, "bucket")
			figureout.Value(s, &c.Region, "region")
		})
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("backend: file\n")))
	require.Error(t, err, "a required section owes its required members everywhere")
	require.Contains(t, err.Error(), "s3.bucket")

	cfg, _, err := d.Resolve(env.Values(map[string]string{"S3_BUCKET": "logs"}))
	require.NoError(t, err)
	require.Equal(t, "logs", cfg.S3.Bucket)

	v, ok := d.Value(&cfg, "s3.bucket")
	require.True(t, ok)
	require.Equal(t, "logs", v)
}

// Absence is spelled by the carrier and nothing else, so a second one anywhere
// below the first has no defensible reading — and neither has a second pointer.
func TestIndirectionIsRefusedWhenItAnswersNothing(t *testing.T) {
	t.Run("CarrierBehindPointer", func(t *testing.T) {
		type cfg struct {
			Timeout *figureout.OptionalOf[int]
		}
		_, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
			figureout.Value(s, &c.Timeout, "timeout")
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "absence has to be spelled once")
	})

	t.Run("PointerToPointer", func(t *testing.T) {
		type cfg struct {
			Retries **int
		}
		_, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
			figureout.Value(s, &c.Retries, "retries")
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "held behind at most one")
	})
}

// A carrier field registered as a plain value names the function to use, so the
// two cannot be mixed up silently.
func TestPlainRegistrarOnCarrier(t *testing.T) {
	type cfg struct {
		Timeout figureout.OptionalOf[int]
	}
	_, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.Value(s, &c.Timeout, "timeout")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "carries optional presence")
	require.Contains(t, err.Error(), "register it with Optional")
}

// A required section is not an optional one, so it cannot be erased: null there
// is a value the field cannot hold rather than a section going away.
func TestRequiredSectionRejectsNull(t *testing.T) {
	d, err := figureout.Derive(describeIndirect)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("s3: null\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be an object")
}
