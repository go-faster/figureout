package jsonschema_test

import (
	"iter"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/go-faster/sdk/gold"
	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
)

func TestMain(m *testing.M) {
	gold.Init()
	os.Exit(m.Run())
}

type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
)

func (LogLevel) AllValues() iter.Seq[LogLevel] {
	return slices.Values([]LogLevel{LogDebug, LogInfo})
}

type Server struct {
	Address string
	Port    int
}

type Config struct {
	Server  Server
	Timeout figureout.OptionalOf[time.Duration]
	Level   LogLevel
	Tags    []string
	Secret  []byte
	Ratio   float64
	Debug   bool
	Workers int
}

var serverDescriptor = figureout.MustDerive(func(c *Server, s *figureout.Schema[Server]) {
	figureout.Explicit(s, &c.Address, "address").NonEmpty().Pattern(`^[a-z0-9.]+$`)
	figureout.Explicit(s, &c.Port, "port",
		env.Name("PORT"),
		jsonschema.Decorate(jsonschema.Patch{Examples: []any{8080}}),
	).InRange(1, 65535)
})

var configDescriptor = figureout.MustDerive(func(c *Config, s *figureout.Schema[Config]) {
	figureout.Object(s, &c.Server, "server", serverDescriptor).
		Doc("HTTP server settings.")
	figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)
	figureout.Enum(s, &c.Level, "level").ApplyDefault(LogInfo)
	figureout.Value(s, &c.Tags, "tags").MinItems(1).MaxItems(8).ApplyDefault([]string{})
	figureout.Value(s, &c.Secret, "secret").MaxLength(64).ApplyDefault([]byte(nil))
	figureout.Value(s, &c.Ratio, "ratio").GreaterThan(0.0).LessThan(1.0).ApplyDefault(0.5)
	figureout.Value(s, &c.Debug, "debug").ApplyDefault(false)
	figureout.Value(s, &c.Workers, "workers",
		figureout.Check("must-be-even", func(v int) error { return nil }),
	).ApplyDefault(4)
})

func TestGenerateSemantic(t *testing.T) {
	data, diags, err := jsonschema.Generate(configDescriptor,
		jsonschema.Semantic(),
		jsonschema.Title("Config"),
	)
	require.NoError(t, err)
	gold.Str(t, string(data), "semantic.json")

	// Rules JSON Schema cannot express are reported, never silently dropped.
	var codes []string
	for _, d := range diags {
		require.Equal(t, figureout.SeverityWarning, d.Severity)
		codes = append(codes, d.Code+" "+d.FieldPath)
	}
	require.ElementsMatch(t, []string{
		jsonschema.CodeNotRepresentable + " timeout",
		figureout.CodeValidatorNotExport + " workers",
	}, codes)
}

func TestStrictExport(t *testing.T) {
	_, diags, err := jsonschema.Generate(configDescriptor, jsonschema.StrictExport())
	require.Error(t, err)
	require.True(t, diags.HasErrors())
}

func TestDecorateRejectsStructuralChange(t *testing.T) {
	type Cfg struct {
		Port int
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port", jsonschema.Decorate(jsonschema.Patch{
			Type: []string{"integer", "string"},
		}))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "use Override")
}

func TestOverrideEmitsDiagnostic(t *testing.T) {
	type Cfg struct {
		Port int
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port", jsonschema.Override(jsonschema.Patch{
			Type: []string{"integer", "string"},
		}))
	})
	require.NoError(t, err)

	data, diags, err := jsonschema.Generate(d)
	require.NoError(t, err)
	require.Len(t, diags, 1)
	require.Equal(t, figureout.SeverityInfo, diags[0].Severity)
	gold.Str(t, string(data), "override.json")
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

func TestGenerateUnion(t *testing.T) {
	data, _, err := jsonschema.Generate(storeDescriptor)
	require.NoError(t, err)
	gold.Str(t, string(data), "union.json")
}

// TestGenerateForSource shows the representation projection: the environment
// source accepts every value as a string, while the semantic schema does not.
func TestGenerateForSource(t *testing.T) {
	type Cfg struct {
		Port    int
		Banner  string
		Timeout figureout.OptionalOf[time.Duration]
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Explicit(s, &c.Port, "port",
			env.Name("PORT"),
			figureout.AcceptShapes(env.Source,
				figureout.Shape{Kind: figureout.ShapeInteger},
				figureout.Shape{Kind: figureout.ShapeString},
			),
		)
		// Zero-defaulted, so an explicit null has something to fall back on
		// and the source schema accepts it, as it does for an optional.
		figureout.Value(s, &c.Banner, "banner",
			figureout.AcceptShapes(env.Source, figureout.Shape{Kind: figureout.ShapeString}),
		)
		// Optional, so an explicit null can erase it; the source schema says
		// so and the semantic schema does not.
		figureout.Optional(s, &c.Timeout, "timeout",
			figureout.AcceptShapes(env.Source, figureout.Shape{Kind: figureout.ShapeString}),
		)
	})
	require.NoError(t, err)

	semantic, _, err := jsonschema.Generate(d, jsonschema.Semantic())
	require.NoError(t, err)
	gold.Str(t, string(semantic), "for_source_semantic.json")

	forEnv, _, err := jsonschema.Generate(d, jsonschema.ForSource(env.Source))
	require.NoError(t, err)
	gold.Str(t, string(forEnv), "for_source_env.json")
}
