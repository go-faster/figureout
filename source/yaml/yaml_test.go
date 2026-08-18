package yaml_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/yaml"
)

type Server struct {
	Address string
	Port    int
}

type Config struct {
	Server  Server
	Timeout figureout.OptionalOf[time.Duration]
	Tags    []string
	Ratio   float64
}

var serverDescriptor = figureout.MustDerive(func(c *Server, s *figureout.Schema[Server]) {
	figureout.Explicit(s, &c.Address, "address").NonEmpty()
	figureout.Explicit(s, &c.Port, "port").InRange(1, 65535)
})

var configDescriptor = figureout.MustDerive(func(c *Config, s *figureout.Schema[Config]) {
	figureout.Object(s, &c.Server, "server", serverDescriptor)
	figureout.Optional(s, &c.Timeout, "timeout")
	figureout.Value(s, &c.Tags, "tags").ApplyDefault([]string{})
	figureout.Value(s, &c.Ratio, "ratio").ApplyDefault(0.0)
})

const document = `server:
  address: 127.0.0.1
  port: 8080
timeout: 5s
tags:
  - a
  - b
ratio: 0.5
`

func TestLoad(t *testing.T) {
	cfg, report, err := configDescriptor.Resolve(yaml.Bytes([]byte(document)))
	require.NoError(t, err)

	require.Equal(t, "127.0.0.1", cfg.Server.Address)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, []string{"a", "b"}, cfg.Tags)
	require.InDelta(t, 0.5, cfg.Ratio, 1e-9)

	timeout, ok := cfg.Timeout.Value()
	require.True(t, ok)
	require.Equal(t, 5*time.Second, timeout)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, yaml.Source, origin.Source)
	require.Equal(t, 3, origin.Line)
	require.Equal(t, 3, origin.Col)
}

func TestFileProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(document), 0o600))

	_, report, err := configDescriptor.Resolve(yaml.File(path))
	require.NoError(t, err)

	origin, ok := report.OriginOf("timeout")
	require.True(t, ok)
	require.Contains(t, origin.String(), "config.yaml:4:1")
}

// TestQuotedScalarIsNotANumber pins the difference from environment
// variables: YAML resolves 8080 and "8080" to different tags, and a config
// language that hides that difference hides typos.
func TestQuotedScalarIsNotANumber(t *testing.T) {
	_, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`server:
  address: localhost
  port: "8080"
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "want !!int, got !!str")
}

// TestIntegerSpellings pins that every spelling YAML resolves as a number reads as the number YAML
// says it is, rather than as base ten or as an error.
func TestIntegerSpellings(t *testing.T) {
	for _, tt := range []struct {
		text string
		want int
	}{
		{"8080", 8080},
		{"+8080", 8080},
		{"1_000", 1000},
		{"0x1f", 31},
		{"0o17", 15},
		{"017", 15},
		{"0b101", 5},
	} {
		t.Run(tt.text, func(t *testing.T) {
			cfg, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(
				"server:\n  address: localhost\n  port: " + tt.text + "\n",
			)))
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.Server.Port)
		})
	}
}

func TestFloatSpellings(t *testing.T) {
	for _, tt := range []struct {
		text string
		want float64
	}{
		{"0.5", 0.5},
		{".5", 0.5},
		{"1e3", 1000},
		{"1_000.5", 1000.5},
		{"7", 7},
	} {
		t.Run(tt.text, func(t *testing.T) {
			cfg, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(
				"server:\n  address: localhost\n  port: 80\nratio: " + tt.text + "\n",
			)))
			require.NoError(t, err)
			require.InDelta(t, tt.want, cfg.Ratio, 1e-9)
		})
	}
}

func TestAnchorsAreResolved(t *testing.T) {
	cfg, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`defaults: &host 127.0.0.1
server:
  address: *host
  port: 80
`)))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", cfg.Server.Address)
}

func TestConstraintErrorCarriesPosition(t *testing.T) {
	_, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`server:
  address: localhost
  port: 70000
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 65535")
	require.Contains(t, err.Error(), "yaml server.port")
}

type S3Backend struct{ Bucket string }

type LocalBackend struct{ Path string }

type Backend struct {
	S3    *S3Backend
	Local *LocalBackend
}

type StoreConfig struct{ Backend Backend }

var storeDescriptor = figureout.MustDerive(func(c *StoreConfig, s *figureout.Schema[StoreConfig]) {
	figureout.OneOf(s, &c.Backend, "backend",
		figureout.Discriminator("type"),
		figureout.Variant("s3", &c.Backend.S3,
			figureout.MustDerive(func(b *S3Backend, s *figureout.Schema[S3Backend]) {
				figureout.Explicit(s, &b.Bucket, "bucket").NonEmpty()
			})),
		figureout.Variant("local", &c.Backend.Local,
			figureout.MustDerive(func(b *LocalBackend, s *figureout.Schema[LocalBackend]) {
				figureout.Explicit(s, &b.Path, "path").NonEmpty()
			})),
	)
})

// TestUnion exercises the inline discriminator layout: the tag is a sibling of
// the variant's own members.
func TestUnion(t *testing.T) {
	cfg, report, err := storeDescriptor.Resolve(yaml.Bytes([]byte(`backend:
  type: s3
  bucket: configs
`)))
	require.NoError(t, err)
	require.NotNil(t, cfg.Backend.S3)
	require.Nil(t, cfg.Backend.Local)
	require.Equal(t, "configs", cfg.Backend.S3.Bucket)

	origin, ok := report.OriginOf("backend.bucket")
	require.True(t, ok)
	require.Equal(t, 3, origin.Line)
}

func TestUnionUnknownVariant(t *testing.T) {
	_, _, err := storeDescriptor.Resolve(yaml.Bytes([]byte(`backend:
  type: gcs
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown variant "gcs", want one of [s3, local]`)
}

func TestUnionDisallowUnknownKeepsDiscriminator(t *testing.T) {
	_, _, err := storeDescriptor.Resolve(yaml.Bytes([]byte(`backend:
  type: s3
  bucket: configs
`), yaml.DisallowUnknownFields()))
	require.NoError(t, err, "the discriminator is claimed by the union, not unknown")
}

func TestSchemaKey(t *testing.T) {
	_, _, err := storeDescriptor.Resolve(yaml.Bytes([]byte(`$schema: ./store.schema.json
backend:
  type: s3
  bucket: configs
`), yaml.DisallowUnknownFields()))
	require.NoError(t, err)

	_, _, err = storeDescriptor.Resolve(yaml.Bytes([]byte(`backend:
  type: s3
  bucket: configs
  $schema: ./store.schema.json
`), yaml.DisallowUnknownFields()))
	require.Error(t, err, "only the document root carries a schema reference")
	require.Contains(t, err.Error(), `unknown configuration property "backend.$schema"`)
}

func TestNamesAndAliases(t *testing.T) {
	type Cfg struct {
		Port   int
		Legacy string
		Hidden string
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port", yaml.Name("listen_port"))
		figureout.Value(s, &c.Legacy, "legacy", yaml.Alias("old_name")).ApplyDefault("")
		figureout.Value(s, &c.Hidden, "hidden", yaml.Skip()).ApplyDefault("untouched")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte(`listen_port: 1
old_name: alias
hidden: ignored
`)))
	require.NoError(t, err)
	require.Equal(t, 1, cfg.Port)
	require.Equal(t, "alias", cfg.Legacy)
	require.Equal(t, "untouched", cfg.Hidden)
}

func TestEmptyDocument(t *testing.T) {
	type Cfg struct {
		Port int
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port").ApplyDefault(80)
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes(nil))
	require.NoError(t, err)
	require.Equal(t, 80, cfg.Port)
}

// TestMergeKey covers the merge key: an anchored mapping is a template several
// entries write themselves against, so "<<" has to be spliced in rather than
// bound as a property named "<<".
func TestMergeKey(t *testing.T) {
	type Site struct {
		Name    string
		Address string
		Port    int
	}
	type Cfg struct {
		Sites []Site
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.ListOf(s, &c.Sites, "sites", func(e *Site, s *figureout.Schema[Site]) {
			figureout.Explicit(s, &e.Name, "name").NonEmpty()
			figureout.Explicit(s, &e.Address, "address").NonEmpty()
			figureout.Value(s, &e.Port, "port").ApplyDefault(80)
		})
	})
	require.NoError(t, err)

	cfg, report, err := d.Resolve(yaml.Bytes([]byte(`sites:
  - &base
    name: first
    address: 127.0.0.1
    port: 8080
  - <<: *base
    name: second
  - <<: *base
    name: third
    port: 9090
`), yaml.DisallowUnknownFields()))
	require.NoError(t, err)
	require.Equal(t, []Site{
		{Name: "first", Address: "127.0.0.1", Port: 8080},
		{Name: "second", Address: "127.0.0.1", Port: 8080},
		{Name: "third", Address: "127.0.0.1", Port: 9090},
	}, cfg.Sites)

	origin, ok := report.OriginOf("sites[1].address")
	require.True(t, ok)
	require.Equal(t, 4, origin.Line, "a merged value is reported where the anchor wrote it")
}

// TestMergeSequence covers a sequence of merges: earlier entries win, the way
// YAML says they do, and a later one still fills what no earlier one set.
func TestMergeSequence(t *testing.T) {
	cfg, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`defaults: &defaults
  address: 0.0.0.0
  port: 80
override: &override
  port: 8080
server:
  <<: [*override, *defaults]
`)))
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0", cfg.Server.Address)
	require.Equal(t, 8080, cfg.Server.Port)
}

func TestMergeNestedAnchor(t *testing.T) {
	cfg, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`base: &base
  address: 0.0.0.0
derived: &derived
  <<: *base
  port: 80
server:
  <<: *derived
  port: 8080
`)))
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0", cfg.Server.Address)
	require.Equal(t, 8080, cfg.Server.Port)
}

func TestMergeScalar(t *testing.T) {
	_, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`server:
  <<: nonsense
  address: 0.0.0.0
  port: 80
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "merge value must be a mapping")
}

func TestMalformedDocument(t *testing.T) {
	_, _, err := configDescriptor.Resolve(yaml.Bytes([]byte("server: [unclosed\n")))
	require.Error(t, err)
}

// TestProjectNames covers the names the source reports for documentation: they
// are the very ones it binds, so a renamed member, an alias, a skipped field
// and a collection element are all spelled the way a document has to write them.
func TestProjectNames(t *testing.T) {
	type Site struct {
		Name string
	}
	type Nested struct {
		Value string
	}
	type Cfg struct {
		Renamed string
		Aliased string
		Skipped string
		Nested  Nested
		Sites   []Site
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Renamed, "renamed", yaml.Name("listen"))
		figureout.Value(s, &c.Aliased, "aliased", yaml.Alias("old"))
		figureout.Value(s, &c.Skipped, "skipped", yaml.Skip())
		figureout.Object(s, &c.Nested, "nested",
			figureout.MustDerive(func(n *Nested, s *figureout.Schema[Nested]) {
				figureout.Value(s, &n.Value, "value")
			}))
		figureout.ListOf(s, &c.Sites, "sites", func(e *Site, s *figureout.Schema[Site]) {
			figureout.Explicit(s, &e.Name, "name").NonEmpty()
		})
	})
	require.NoError(t, err)

	namer, ok := yaml.File("").(figureout.SourceNamer)
	require.True(t, ok)

	require.Equal(t, map[string][]string{
		"renamed":      {"listen"},
		"aliased":      {"aliased", "old"},
		"nested":       {"nested"},
		"nested.value": {"nested.value"},
		"sites":        {"sites"},
		"sites[].name": {"sites[].name"},
	}, namer.ProjectNames(d.Model()))
}
