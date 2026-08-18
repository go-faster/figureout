package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type s3Section struct {
	Bucket string
	Region string
}

type storageConfig struct {
	Backend              string
	EnableNegativeOffset *bool
	Retries              *int
	S3                   *s3Section
}

func describeStorage(c *storageConfig, s *figureout.Schema[storageConfig]) {
	figureout.Value(s, &c.Backend, "backend")
	figureout.OptionalPtr(s, &c.EnableNegativeOffset, "enable_negative_offset")
	figureout.OptionalPtr(s, &c.Retries, "retries")
	figureout.OptionalObjectFunc(s, &c.S3, "s3", func(c *s3Section, s *figureout.Schema[s3Section]) {
		figureout.Value(s, &c.Bucket, "bucket")
		figureout.Value(s, &c.Region, "region")
	})
}

func ptr[T any](v T) *T { return &v }

// TestPointerScalarThreeStates is the whole point of a "*bool": nil, false and
// true are three answers, and only two of them are a value.
func TestPointerScalarThreeStates(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("enable_negative_offset")
	require.True(t, ok)
	require.Equal(t, figureout.PresencePointer, f.Presence)
	require.False(t, f.Required(), "a carrier says absence is allowed by construction")

	for _, tt := range []struct {
		name string
		doc  string
		want *bool
	}{
		{"Absent", "backend: s3\n", nil},
		{"False", "enable_negative_offset: false\n", ptr(false)},
		{"True", "enable_negative_offset: true\n", ptr(true)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := d.Resolve(yaml.Bytes([]byte(tt.doc)))
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.EnableNegativeOffset)
		})
	}
}

// TestPointerScalarFromEnv covers a flat source, which reaches a pointer field
// through the same accessor.
func TestPointerScalarFromEnv(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"RETRIES": "3"}))
	require.NoError(t, err)
	require.Equal(t, ptr(3), cfg.Retries)
	require.Nil(t, cfg.EnableNegativeOffset)

	_, _, err = d.Resolve(env.Values(map[string]string{"RETRIES": "many"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid integer "many"`)
}

// TestPointerScalarErased covers the merge directive: a later layer that writes
// null takes the value away rather than writing a pointer to the zero value.
func TestPointerScalarErased(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	cfg, rep, err := d.Resolve(
		yaml.Bytes([]byte("enable_negative_offset: true\n")),
		yaml.Bytes([]byte("enable_negative_offset: null\n")),
	)
	require.NoError(t, err)
	require.Nil(t, cfg.EnableNegativeOffset)
	_, erased := rep.ErasedBy("enable_negative_offset")
	require.True(t, erased)
}

// TestPointerScalarConstraintsAreTypedAsTheElement checks that the fluent
// builder is typed as T rather than as *T, and that the constraint runs.
func TestPointerScalarConstraintsAreTypedAsTheElement(t *testing.T) {
	d, err := figureout.Derive(func(c *storageConfig, s *figureout.Schema[storageConfig]) {
		figureout.Value(s, &c.Backend, "backend")
		figureout.OptionalPtr(s, &c.EnableNegativeOffset, "enable_negative_offset")
		figureout.OptionalPtr(s, &c.Retries, "retries").InRange(1, 10)
		figureout.Ignore(s, &c.S3)
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("retries: 99\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "retries")
}

// TestPointerScalarDefaultMakesItPresent covers ApplyDefault on a pointer, which
// has to allocate rather than leave nil.
func TestPointerScalarDefaultMakesItPresent(t *testing.T) {
	d, err := figureout.Derive(func(c *storageConfig, s *figureout.Schema[storageConfig]) {
		figureout.Value(s, &c.Backend, "backend")
		figureout.OptionalPtr(s, &c.EnableNegativeOffset, "enable_negative_offset").ApplyDefault(true)
		figureout.OptionalPtr(s, &c.Retries, "retries")
		figureout.Ignore(s, &c.S3)
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("backend: s3\n")))
	require.NoError(t, err)
	require.Equal(t, ptr(true), cfg.EnableNegativeOffset)

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("enable_negative_offset: false\n")))
	require.NoError(t, err)
	require.Equal(t, ptr(false), cfg.EnableNegativeOffset, "a default never wins over a value")
}

// TestOptionalObjectDistinguishesAbsentFromEmpty is what a zero struct cannot
// say: an empty section is a section.
func TestOptionalObjectDistinguishesAbsentFromEmpty(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("backend: s3\n")))
	require.NoError(t, err)
	require.Nil(t, cfg.S3, "no section")

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("s3: {}\n")))
	require.NoError(t, err)
	require.Equal(t, &s3Section{}, cfg.S3, "a section, defaulted throughout")

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("s3:\n  bucket: logs\n")))
	require.NoError(t, err)
	require.Equal(t, &s3Section{Bucket: "logs"}, cfg.S3)
}

// TestOptionalObjectFromEnv covers a source that has no name for the section
// itself: the members it did read are the whole of what it can say.
func TestOptionalObjectFromEnv(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"S3_BUCKET": "logs"}))
	require.NoError(t, err)
	require.Equal(t, &s3Section{Bucket: "logs"}, cfg.S3)

	cfg, _, err = d.Resolve(env.Values(map[string]string{"BACKEND": "file"}))
	require.NoError(t, err)
	require.Nil(t, cfg.S3)
}

// TestOptionalObjectErased covers null on a section: it takes the section away,
// and what earlier layers put in it with it.
func TestOptionalObjectErased(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(
		yaml.Bytes([]byte("s3:\n  bucket: logs\n  region: eu\n")),
		yaml.Bytes([]byte("s3: null\n")),
	)
	require.NoError(t, err)
	require.Nil(t, cfg.S3, "the members of a section that is gone are not a section")

	cfg, _, err = d.Resolve(
		yaml.Bytes([]byte("s3:\n  bucket: logs\n  region: eu\n")),
		yaml.Bytes([]byte("s3: null\n")),
		yaml.Bytes([]byte("s3:\n  bucket: traces\n")),
	)
	require.NoError(t, err)
	require.Equal(t, &s3Section{Bucket: "traces"}, cfg.S3)
}

// TestOptionalObjectDemandsMembersOnlyWhenPresent covers what makes "required
// inside an optional section" mean anything.
func TestOptionalObjectDemandsMembersOnlyWhenPresent(t *testing.T) {
	d, err := figureout.Derive(func(c *storageConfig, s *figureout.Schema[storageConfig]) {
		figureout.Value(s, &c.Backend, "backend")
		figureout.Ignore(s, &c.EnableNegativeOffset)
		figureout.Ignore(s, &c.Retries)
		figureout.OptionalObjectFunc(s, &c.S3, "s3", func(c *s3Section, s *figureout.Schema[s3Section]) {
			figureout.Explicit(s, &c.Bucket, "bucket").NonEmpty()
			figureout.Value(s, &c.Region, "region")
		})
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("backend: file\n")))
	require.NoError(t, err, "no section, so nothing inside it is demanded")
	require.Nil(t, cfg.S3)

	_, _, err = d.Resolve(yaml.Bytes([]byte("s3:\n  region: eu\n")))
	require.Error(t, err, "a section that is there owes its required members")
	require.Contains(t, err.Error(), "s3.bucket")
}

// TestOptionalObjectRejectsAScalar covers a section written as something that
// is not one.
func TestOptionalObjectRejectsAScalar(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("s3: bucket\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be an object")
}

// TestPointerRegistrarMismatch covers the diagnostics: each registrar takes one
// carrier, and says which one to use instead.
func TestPointerRegistrarMismatch(t *testing.T) {
	t.Run("ValueOnPointer", func(t *testing.T) {
		_, err := figureout.Derive(func(c *storageConfig, s *figureout.Schema[storageConfig]) {
			figureout.Value(s, &c.Backend, "backend")
			figureout.Value(s, &c.EnableNegativeOffset, "enable_negative_offset")
			figureout.Ignore(s, &c.Retries)
			figureout.Ignore(s, &c.S3)
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "carries pointer presence")
		require.Contains(t, err.Error(), "OptionalPtr")
	})

	// Type inference already stops "ObjectFunc(s, &c.S3, …)" on a pointer field
	// at compile time, which is where a mistake is cheapest. Spelled out, the
	// registrar still has to report it rather than build a nested descriptor
	// rooted at a pointer.
	t.Run("ObjectFuncOnPointer", func(t *testing.T) {
		_, err := figureout.Derive(func(c *storageConfig, s *figureout.Schema[storageConfig]) {
			figureout.Value(s, &c.Backend, "backend")
			figureout.Ignore(s, &c.EnableNegativeOffset)
			figureout.Ignore(s, &c.Retries)
			figureout.ObjectFunc[storageConfig, *s3Section](s, &c.S3, "s3",
				func(**s3Section, *figureout.Schema[*s3Section]) {})
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "OptionalObject or OptionalObjectFunc")
	})

	t.Run("OptionalOnPointer", func(t *testing.T) {
		type Cfg struct {
			Timeout *figureout.OptionalOf[int]
		}
		_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
			figureout.OptionalPtr(s, &c.Timeout, "timeout")
		})
		require.Error(t, err, "a carrier behind a pointer is two answers to one question")
	})
}

// TestOptionalObjectSchema covers generation: an optional section is never in a
// parent's required list, and its own required members still are.
func TestOptionalObjectSchema(t *testing.T) {
	d, err := figureout.Derive(func(c *storageConfig, s *figureout.Schema[storageConfig]) {
		figureout.Value(s, &c.Backend, "backend")
		figureout.Ignore(s, &c.EnableNegativeOffset)
		figureout.Ignore(s, &c.Retries)
		figureout.OptionalObjectFunc(s, &c.S3, "s3", func(c *s3Section, s *figureout.Schema[s3Section]) {
			figureout.Explicit(s, &c.Bucket, "bucket")
			figureout.Value(s, &c.Region, "region")
		})
	})
	require.NoError(t, err)

	out, diags, err := jsonschema.Generate(d)
	require.NoError(t, err)
	require.Empty(t, diags)
	require.Contains(t, string(out), `"bucket"`)
	require.NotContains(t, string(out), `"s3"
      ]`)
}

// TestPointerValueLookup covers Descriptor.Value through a pointer, which must
// report false rather than read through a nil one.
func TestPointerValueLookup(t *testing.T) {
	d, err := figureout.Derive(describeStorage)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("backend: file\n")))
	require.NoError(t, err)

	_, ok := d.Value(&cfg, "s3.bucket")
	require.False(t, ok, "no section")
	_, ok = d.Value(&cfg, "enable_negative_offset")
	require.False(t, ok, "no value")

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("s3:\n  bucket: logs\nenable_negative_offset: false\n")))
	require.NoError(t, err)

	v, ok := d.Value(&cfg, "s3.bucket")
	require.True(t, ok)
	require.Equal(t, "logs", v)
	v, ok = d.Value(&cfg, "enable_negative_offset")
	require.True(t, ok)
	require.Equal(t, false, v, "a present false is a value, not an absence")
}
