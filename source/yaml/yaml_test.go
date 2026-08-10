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

func TestMalformedDocument(t *testing.T) {
	_, _, err := configDescriptor.Resolve(yaml.Bytes([]byte("server: [unclosed\n")))
	require.Error(t, err)
}
