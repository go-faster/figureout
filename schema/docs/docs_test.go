package docs_test

import (
	"context"
	"iter"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/go-faster/sdk/gold"
	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/docs"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

func TestMain(m *testing.M) {
	gold.Init()
	os.Exit(m.Run())
}

// LogLevel enumerates its own values, so the allowed values have one source of
// truth.
type LogLevel string

// Log levels.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
)

// AllValues implements [figureout.EnumValuer].
func (LogLevel) AllValues() iter.Seq[LogLevel] {
	return slices.Values([]LogLevel{LogDebug, LogInfo})
}

type Server struct {
	Address string
	Port    int
	Timeout time.Duration
}

type Site struct {
	Name     string
	MaxBytes int
}

type S3Storage struct{ Bucket string }

type LocalStorage struct{ Path string }

type Storage struct {
	S3    *S3Storage
	Local *LocalStorage
}

type Config struct {
	Server   Server
	Storage  Storage
	Sites    []Site
	Level    LogLevel
	Token    string
	Internal int
	Tags     []string
	Retries  figureout.OptionalOf[int]
	Region   string
	Verbose  bool
}

var serverDescriptor = figureout.MustDerive(func(c *Server, s *figureout.Schema[Server]) {
	figureout.Value(s, &c.Address, "address").
		Doc("Listen address.").NonEmpty().Pattern(`^[a-z0-9.]+$`).ApplyDefault("127.0.0.1")
	figureout.Value(s, &c.Port, "port", env.Name("LISTEN_PORT")).
		Doc("Listen port.").InRange(1, 65535).ApplyDefault(8080).
		Examples(8080, 9090)
	figureout.Value(s, &c.Timeout, "timeout", figureout.Unit(time.Second)).
		Doc("Request timeout.").AtLeast(time.Second).ApplyDefault(30 * time.Second)
})

var configDescriptor = figureout.MustDerive(func(c *Config, s *figureout.Schema[Config]) {
	figureout.Object(s, &c.Server, "server", serverDescriptor).
		Doc("HTTP server settings.")

	figureout.OneOf(s, &c.Storage, "storage",
		figureout.Discriminator("type"),
		figureout.Variant("s3", &c.Storage.S3,
			figureout.MustDerive(func(b *S3Storage, s *figureout.Schema[S3Storage]) {
				figureout.Explicit(s, &b.Bucket, "bucket").NonEmpty()
			})),
		figureout.Variant("local", &c.Storage.Local,
			figureout.MustDerive(func(b *LocalStorage, s *figureout.Schema[LocalStorage]) {
				figureout.Explicit(s, &b.Path, "path").NonEmpty()
			})),
	).Doc("Where the service keeps its data.")

	figureout.ListOf(s, &c.Sites, "sites", func(e *Site, s *figureout.Schema[Site]) {
		figureout.Explicit(s, &e.Name, "name").NonEmpty()
		figureout.Value(s, &e.MaxBytes, "max_bytes").AtMost(1 << 20).ApplyDefault(1024)
	}).Doc("Served sites.")

	figureout.Enum(s, &c.Level, "level").Doc("Log verbosity.").ApplyDefault(LogInfo)

	figureout.Value(s, &c.Token, "token", figureout.Secret()).
		Doc("API token.").ApplyDefault("s3cret")

	figureout.Value(s, &c.Internal, "internal").Hidden().ApplyDefault(0)

	figureout.Value(s, &c.Tags, "tags",
		figureout.Check("sorted", func([]string) error { return nil }),
	).Doc("Tags applied to every metric.").MaxItems(8).ApplyDefault([]string{})

	figureout.Optional(s, &c.Retries, "retries").
		Doc("Retry budget; unset means the client default.").DocumentDefault(3)

	figureout.Value(s, &c.Region, "region", figureout.MovedFrom("legacy_region")).
		Doc("Deployment region.").ApplyDefault("eu-west-1")

	figureout.Value(s, &c.Verbose, "verbose").
		Doc("Log every request.").Deprecated("set level to debug instead.").ApplyDefault(false)
})

// TestGenerate documents the semantic configuration, with no source column: a
// page that names no source describes the values, not their spellings.
func TestGenerate(t *testing.T) {
	data, diags, err := docs.Generate(configDescriptor, docs.Title("Reference"))
	require.NoError(t, err)
	gold.Str(t, string(data), "semantic.md")

	// A rule with no prose form is reported, never silently dropped.
	require.Len(t, diags, 1)
	require.Equal(t, figureout.SeverityWarning, diags[0].Severity)
	require.Equal(t, docs.CodeNotDocumentable, diags[0].Code)
	require.Equal(t, "tags", diags[0].FieldPath)
}

// TestGenerateForSources adds a column per source, whose names come from the
// configured source itself: the prefix is part of the variable.
func TestGenerateForSources(t *testing.T) {
	data, _, err := docs.Generate(configDescriptor,
		docs.Title("Reference"),
		docs.ForSource(yaml.File("")),
		docs.ForSource(env.Values(nil, env.Prefix("APP_"))),
	)
	require.NoError(t, err)
	gold.Str(t, string(data), "sources.md")
}

func TestStrictExport(t *testing.T) {
	_, diags, err := docs.Generate(configDescriptor, docs.StrictExport())
	require.Error(t, err)
	require.True(t, diags.HasErrors())
}

func TestHiddenIsOmitted(t *testing.T) {
	page, _, err := docs.Build(configDescriptor)
	require.NoError(t, err)

	for _, s := range page.Sections {
		for _, f := range s.Fields {
			require.NotEqual(t, "internal", f.Name, "a hidden field is not documented")
		}
	}
}

// TestDeprecatedIsMarked shows that a deprecated field is marked rather than
// dropped: an operator reading the page has to find the key they wrote.
//
// A former spelling created by MovedFrom is a different case: the core marks
// its shadow hidden, so the page documents the current spelling only.
func TestDeprecatedIsMarked(t *testing.T) {
	page, _, err := docs.Build(configDescriptor)
	require.NoError(t, err)

	require.NotEmpty(t, fieldOf(t, page, "verbose").Deprecated)
	for _, s := range page.Sections {
		for _, f := range s.Fields {
			require.NotEqual(t, "legacy_region", f.Name)
		}
	}
}

// TestAnchorsAreUnique keeps deep links working: every section is addressable,
// and a link on a row points at the section it expands into.
func TestAnchorsAreUnique(t *testing.T) {
	page, _, err := docs.Build(configDescriptor)
	require.NoError(t, err)

	anchors := map[string]bool{}
	for _, s := range page.Sections {
		require.NotEmpty(t, s.Anchor)
		require.False(t, anchors[s.Anchor], "duplicate anchor %q", s.Anchor)
		anchors[s.Anchor] = true
	}
	for _, s := range page.Sections {
		for _, f := range s.Fields {
			if f.Section != "" {
				require.True(t, anchors[f.Section], "%s links to a missing section", f.Path)
			}
		}
	}
}

// TestSecretIsNamedButNeverValued follows the core's stance: Secret redacts
// values rather than names, so a credential is documented and its value is not.
func TestSecretIsNamedButNeverValued(t *testing.T) {
	data, _, err := docs.Generate(configDescriptor)
	require.NoError(t, err)
	require.NotContains(t, string(data), "s3cret", "a documented value is a leaked one")
	require.Contains(t, string(data), "token", "an operator cannot supply what is not named")
	require.Contains(t, string(data), "**Secret.**", "the row must say it carries a credential")
}

// TestUnionRendersEveryVariant covers the tag that selects each one, which is
// configuration without being a Go field.
func TestUnionRendersEveryVariant(t *testing.T) {
	page, _, err := docs.Build(configDescriptor)
	require.NoError(t, err)

	tags := map[string]string{}
	for _, s := range page.Sections {
		if s.Variant != "" {
			tags[s.Variant] = s.Discriminator
		}
	}
	require.Equal(t, map[string]string{"s3": "type", "local": "type"}, tags)
}

// unnamed is a source that cannot report the names it accepts.
type unnamed struct{}

func (unnamed) ID() figureout.SourceID { return figureout.SourceID("unnamed") }

func (unnamed) Load(context.Context, *figureout.Model) (*figureout.Layer, error) {
	return &figureout.Layer{Source: figureout.SourceID("unnamed")}, nil
}

// TestSourceWithoutNames reports the omission rather than emitting an empty
// column that would read as "this source accepts nothing".
func TestSourceWithoutNames(t *testing.T) {
	page, diags, err := docs.Build(configDescriptor, docs.ForSource(unnamed{}))
	require.NoError(t, err)
	require.Empty(t, page.Sources)
	require.Len(t, diags, 2)
	require.Contains(t, []string{diags[0].Code, diags[1].Code}, docs.CodeSourceNotNamed)
}

func fieldOf(t *testing.T, page *docs.Page, name string) *docs.Field {
	t.Helper()
	for _, s := range page.Sections {
		for _, f := range s.Fields {
			if f.Name == name {
				return f
			}
		}
	}
	t.Fatalf("field %q is not documented", name)
	return nil
}
