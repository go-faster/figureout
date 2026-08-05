package figureout_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/yaml"
)

type fetchConfig struct {
	Sites   []string
	Proxies map[string]string
	Agentic bool
	APIKey  string
}

func fetchDescriptor(t *testing.T) *figureout.Descriptor[fetchConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *fetchConfig, s *figureout.Schema[fetchConfig]) {
		figureout.Value(s, &c.Sites, "sites").ApplyDefault([]string{})
		figureout.Value(s, &c.Proxies, "proxies").ApplyDefault(map[string]string{})
		figureout.Value(s, &c.Agentic, "agentic").ApplyDefault(false)
		figureout.Value(s, &c.APIKey, "api_key").ApplyDefault("")

		figureout.Invariant(s, "proxy-exists", func(c *fetchConfig) error {
			var errs []error
			for i, site := range c.Sites {
				if _, ok := c.Proxies[site]; !ok {
					errs = append(errs, figureout.At(fmt.Sprintf("sites[%d]", i)).
						Errorf("no proxy named %q is configured", site))
				}
			}
			return errors.Join(errs...)
		})

		figureout.Invariant(s, "agentic-needs-key", func(c *fetchConfig) error {
			if c.Agentic && c.APIKey == "" {
				return figureout.At("agentic", "api_key").
					Errorf("agentic fetching needs an API key")
			}
			return nil
		})
	})
	require.NoError(t, err)
	return d
}

func TestInvariantHolds(t *testing.T) {
	cfg, report, err := fetchDescriptor(t).Resolve(yaml.Bytes([]byte(`
sites: [docs]
proxies: {docs: "http://proxy"}
`)))
	require.NoError(t, err)
	require.Empty(t, report.Diagnostics)
	require.Equal(t, []string{"docs"}, cfg.Sites)
}

func TestInvariantViolationKeepsProvenance(t *testing.T) {
	_, report, err := fetchDescriptor(t).Resolve(yaml.Bytes([]byte(`
sites: [docs]
proxies: {}
`)))
	require.Error(t, err)

	require.Len(t, report.Diagnostics, 1)
	d := report.Diagnostics[0]
	require.Equal(t, figureout.CodeInvariantViolated, d.Code)
	require.Equal(t, "sites[0]", d.FieldPath)
	require.Equal(t, `no proxy named "docs" is configured`, d.Message)
	require.NotNil(t, d.Origin, "the path carries the origin the report already knew")
	require.Equal(t, 2, d.Origin.Line)
}

func TestInvariantReportsSeveralViolations(t *testing.T) {
	_, report, err := fetchDescriptor(t).Resolve(yaml.Bytes([]byte(`
sites: [docs, wiki]
proxies: {}
`)))
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 2, "errors.Join reports one diagnostic per violation")
	require.Equal(t, "sites[0]", report.Diagnostics[0].FieldPath)
	require.Equal(t, "sites[1]", report.Diagnostics[1].FieldPath)
}

func TestInvariantNamesSeveralPaths(t *testing.T) {
	_, report, err := fetchDescriptor(t).Resolve(yaml.Bytes([]byte(`agentic: true`)))
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "agentic", report.Diagnostics[0].FieldPath)
	require.Contains(t, report.Diagnostics[0].Message, "(see also api_key)")
	require.Equal(t, "fetchConfig.Agentic", report.Diagnostics[0].GoPath)
}

func TestInvariantSkippedAfterFieldError(t *testing.T) {
	// A constraint failure stops before the invariants, so a violation is never
	// a consequence of an error already reported.
	d, err := figureout.Derive(func(c *fetchConfig, s *figureout.Schema[fetchConfig]) {
		figureout.Value(s, &c.Sites, "sites").MaxItems(1).ApplyDefault([]string{})
		figureout.Value(s, &c.Proxies, "proxies").ApplyDefault(map[string]string{})
		figureout.Value(s, &c.Agentic, "agentic").ApplyDefault(false)
		figureout.Value(s, &c.APIKey, "api_key").ApplyDefault("")
		figureout.Invariant(s, "never-runs", func(*fetchConfig) error {
			return errors.New("should not run")
		})
	})
	require.NoError(t, err)

	_, report, err := d.Resolve(yaml.Bytes([]byte(`sites: [a, b]`)))
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, figureout.CodeConstraintMismatch, report.Diagnostics[0].Code)
}

func TestInvariantPlainError(t *testing.T) {
	d, err := figureout.Derive(func(c *fetchConfig, s *figureout.Schema[fetchConfig]) {
		figureout.IgnoreRecursive(s, &c.Sites)
		figureout.IgnoreRecursive(s, &c.Proxies)
		figureout.Ignore(s, &c.Agentic)
		figureout.Ignore(s, &c.APIKey)
		figureout.Invariant(s, "always-fails", func(*fetchConfig) error {
			return errors.New("nope")
		})
	})
	require.NoError(t, err)

	_, report, err := d.Resolve()
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "nope", report.Diagnostics[0].Message)
	require.Empty(t, report.Diagnostics[0].FieldPath, "a plain error has no path to attach")
}

func TestInvariantModel(t *testing.T) {
	require.Equal(t, []figureout.InvariantModel{
		{Name: "proxy-exists"},
		{Name: "agentic-needs-key"},
	}, fetchDescriptor(t).Model().Invariants())
}

func TestInvariantNested(t *testing.T) {
	type limits struct{ Min, Max int }
	type cfg struct{ Limits limits }

	d, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.ObjectFunc(s, &c.Limits, "limits", func(c *limits, s *figureout.Schema[limits]) {
			figureout.Value(s, &c.Min, "min")
			figureout.Value(s, &c.Max, "max")
			figureout.Invariant(s, "ordered", func(c *limits) error {
				if c.Min > c.Max {
					return figureout.At("min").Errorf("min %d exceeds max %d", c.Min, c.Max)
				}
				return nil
			})
		})
	})
	require.NoError(t, err)
	require.Equal(t, []figureout.InvariantModel{{Name: "limits.ordered"}}, d.Model().Invariants())

	_, report, err := d.Resolve(yaml.Bytes([]byte(`
limits:
  min: 10
  max: 1
`)))
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "limits.min", report.Diagnostics[0].FieldPath,
		"a nested schema's violation paths are qualified by the nested object")
}

func TestInvariantDuplicateName(t *testing.T) {
	_, err := figureout.Derive(func(c *fetchConfig, s *figureout.Schema[fetchConfig]) {
		figureout.IgnoreRecursive(s, &c.Sites)
		figureout.IgnoreRecursive(s, &c.Proxies)
		figureout.Ignore(s, &c.Agentic)
		figureout.Ignore(s, &c.APIKey)
		figureout.Invariant(s, "dup", func(*fetchConfig) error { return nil })
		figureout.Invariant(s, "dup", func(*fetchConfig) error { return nil })
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "registered more than once")
}
