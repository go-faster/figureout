package figureout_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

type zeroConfig struct {
	URL        string
	MaxResults int
	Insecure   bool
	Timeout    time.Duration
}

func zeroDescriptor(t *testing.T) *figureout.Descriptor[zeroConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *zeroConfig, s *figureout.Schema[zeroConfig]) {
		figureout.Value(s, &c.URL, "url", env.NullLiteral("null"))
		figureout.Value(s, &c.MaxResults, "max_results")
		figureout.Value(s, &c.Insecure, "insecure")
		figureout.Value(s, &c.Timeout, "timeout")
	})
	require.NoError(t, err)
	return d
}

func TestValueResolvesAbsenceToZero(t *testing.T) {
	cfg, report, err := zeroDescriptor(t).Resolve(env.Values(map[string]string{
		"URL": "https://jira.example",
	}))
	require.NoError(t, err)

	require.Equal(t, "https://jira.example", cfg.URL)
	require.Zero(t, cfg.MaxResults)
	require.False(t, cfg.Insecure)
	require.Zero(t, cfg.Timeout)

	origin, ok := report.OriginOf("max_results")
	require.True(t, ok, "a zero the descriptor decided on still has an origin")
	require.Equal(t, figureout.SourceID("default"), origin.Source)
}

func TestValueIsNotRequired(t *testing.T) {
	d := zeroDescriptor(t)
	f, ok := d.Model().FieldByPath("url")
	require.True(t, ok)
	require.False(t, f.Required())
	require.True(t, f.ZeroDefault())
}

func TestValueErasedByNullResolvesToZero(t *testing.T) {
	cfg, _, err := zeroDescriptor(t).Resolve(
		env.Values(map[string]string{"URL": "https://jira.example"}),
		env.Values(map[string]string{"URL": "null"}),
	)
	require.NoError(t, err, "erasing leaves the zero value, not a missing one")
	require.Empty(t, cfg.URL)
}

func TestExplicitRequiresAValue(t *testing.T) {
	type Cfg struct {
		DSN string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Explicit(s, &c.DSN, "dsn")
	})
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("dsn")
	require.True(t, ok)
	require.True(t, f.Required())
	require.False(t, f.ZeroDefault())

	_, _, err = d.Resolve(env.Values(nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value provided and no default")
}

func TestValueRequiredOptsBackIn(t *testing.T) {
	type Cfg struct {
		DSN string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.DSN, "dsn").Required()
	})
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("dsn")
	require.True(t, ok)
	require.True(t, f.Required(), "Value(...).Required() is Explicit spelled the long way")

	_, _, err = d.Resolve(env.Values(nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value provided and no default")
}

func TestValueDefaultReplacesTheZero(t *testing.T) {
	type Cfg struct {
		Retries int
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Retries, "retries").ApplyDefault(3)
	})
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("retries")
	require.True(t, ok)
	require.False(t, f.ZeroDefault(), "an applied default is what absence resolves to")

	cfg, _, err := d.Resolve(env.Values(nil))
	require.NoError(t, err)
	require.Equal(t, 3, cfg.Retries)
}

func TestValueDocumentedDefaultKeepsTheZero(t *testing.T) {
	type Cfg struct {
		Retries int
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Retries, "retries").DocumentDefault(3)
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(nil))
	require.NoError(t, err, "documenting a default never makes a field required")
	require.Zero(t, cfg.Retries, "and never changes what absence resolves to")
}

func TestValueRejectsAZeroItsConstraintsRefuse(t *testing.T) {
	type Cfg struct {
		Name  string
		Port  int
		Level string
	}

	for _, tt := range []struct {
		name     string
		describe func(*Cfg, *figureout.Schema[Cfg])
		contains string
	}{
		{
			name: "NonEmpty",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Value(s, &c.Name, "name").NonEmpty()
			},
			contains: "length must be at least 1",
		},
		{
			name: "InRange",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Value(s, &c.Port, "port").InRange(1, 65535)
			},
			contains: "must be at least 1",
		},
		{
			name: "Enum",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Value(s, &c.Level, "level").Enum("debug", "info")
			},
			contains: "must be one of",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := figureout.Derive(tt.describe,
				figureout.Completeness(figureout.CompletenessDisabled))
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.contains)
			require.Contains(t, err.Error(), "register it with Explicit",
				"the diagnostic names the way out")
		})
	}
}

func TestExplicitAcceptsAZeroRejectingConstraint(t *testing.T) {
	type Cfg struct {
		Name string
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Explicit(s, &c.Name, "name").NonEmpty()
	})
	require.NoError(t, err, "nothing falls back to the zero value, so nothing rejects it")
}
