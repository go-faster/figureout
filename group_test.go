package figureout_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

// gitLabConfig is the flat Go shape a consumer wants; the file nests it.
type gitLabConfig struct {
	WebhookEnabled bool
	WebhookSecret  string
	PollInterval   time.Duration
	Token          string
}

func gitLabDescriptor(t *testing.T) *figureout.Descriptor[gitLabConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *gitLabConfig, s *figureout.Schema[gitLabConfig]) {
		figureout.Value(s, &c.Token, "token", figureout.Hidden())
		figureout.Group(s, "webhook", func(s *figureout.Schema[gitLabConfig]) {
			figureout.Value(s, &c.WebhookEnabled, "enabled").ApplyDefault(false)
			figureout.Value(s, &c.WebhookSecret, "secret").ApplyDefault("")
		})
		figureout.Group(s, "poll", func(s *figureout.Schema[gitLabConfig]) {
			figureout.Value(s, &c.PollInterval, "interval").ApplyDefault(time.Minute)
		})
	})
	require.NoError(t, err)
	return d
}

func TestGroupModel(t *testing.T) {
	m := gitLabDescriptor(t).Model()

	f, ok := m.FieldByPath("webhook.secret")
	require.True(t, ok)
	require.Equal(t, figureout.TypeString, f.Type.Kind)
	require.Equal(t, "gitLabConfig.WebhookSecret", f.GoName,
		"a group nests the path, not the Go field")

	g, ok := m.FieldByPath("webhook")
	require.True(t, ok)
	require.Equal(t, figureout.TypeObject, g.Type.Kind)
	require.Len(t, g.Type.Object.Fields, 2)
}

func TestGroupYAML(t *testing.T) {
	cfg, report, err := gitLabDescriptor(t).Resolve(yaml.Bytes([]byte(`
token: glpat-secret
webhook:
  enabled: true
  secret: hunter2
poll:
  interval: 30s
`)))
	require.NoError(t, err)
	require.True(t, cfg.WebhookEnabled)
	require.Equal(t, "hunter2", cfg.WebhookSecret)
	require.Equal(t, 30*time.Second, cfg.PollInterval)
	require.Equal(t, "glpat-secret", cfg.Token)

	origin, ok := report.OriginOf("webhook.secret")
	require.True(t, ok)
	require.Equal(t, "webhook.secret", origin.Name)
	require.Equal(t, 5, origin.Line, "provenance points at the nested key")
}

func TestGroupEnv(t *testing.T) {
	cfg, report, err := gitLabDescriptor(t).Resolve(env.Values(map[string]string{
		"GITLAB_TOKEN":          "glpat-secret",
		"GITLAB_WEBHOOK_SECRET": "hunter2",
	}, env.Prefix("GITLAB_")))
	require.NoError(t, err)
	require.Equal(t, "hunter2", cfg.WebhookSecret)
	require.Equal(t, time.Minute, cfg.PollInterval, "default still applies")

	origin, ok := report.OriginOf("webhook.secret")
	require.True(t, ok)
	require.Equal(t, "GITLAB_WEBHOOK_SECRET", origin.Name,
		"a group contributes a segment, so nesting composes")
}

func TestGroupCompleteness(t *testing.T) {
	// Token is never registered: the group does not hide it.
	_, err := figureout.Derive(func(c *gitLabConfig, s *figureout.Schema[gitLabConfig]) {
		figureout.Group(s, "webhook", func(s *figureout.Schema[gitLabConfig]) {
			figureout.Value(s, &c.WebhookEnabled, "enabled")
			figureout.Value(s, &c.WebhookSecret, "secret")
		})
		figureout.Ignore(s, &c.PollInterval)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "gitLabConfig.Token")
}

func TestGroupDuplicateField(t *testing.T) {
	// The same Go field inside and outside a group is still a duplicate.
	_, err := figureout.Derive(func(c *gitLabConfig, s *figureout.Schema[gitLabConfig]) {
		figureout.Value(s, &c.WebhookSecret, "secret")
		figureout.Group(s, "webhook", func(s *figureout.Schema[gitLabConfig]) {
			figureout.Value(s, &c.WebhookSecret, "secret")
		})
		figureout.Ignore(s, &c.WebhookEnabled)
		figureout.Ignore(s, &c.PollInterval)
		figureout.Ignore(s, &c.Token)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeDuplicateField)
}

func TestGroupNameCollision(t *testing.T) {
	_, err := figureout.Derive(func(c *gitLabConfig, s *figureout.Schema[gitLabConfig]) {
		figureout.Value(s, &c.Token, "webhook")
		figureout.Group(s, "webhook", func(s *figureout.Schema[gitLabConfig]) {
			figureout.Value(s, &c.WebhookSecret, "secret")
		})
		figureout.Ignore(s, &c.WebhookEnabled)
		figureout.Ignore(s, &c.PollInterval)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeDuplicateName)
}

func TestGroupNested(t *testing.T) {
	// Sibling names in different groups do not collide.
	d, err := figureout.Derive(func(c *gitLabConfig, s *figureout.Schema[gitLabConfig]) {
		figureout.Ignore(s, &c.Token)
		figureout.Ignore(s, &c.WebhookEnabled)
		figureout.Group(s, "gitlab", func(s *figureout.Schema[gitLabConfig]) {
			figureout.Group(s, "webhook", func(s *figureout.Schema[gitLabConfig]) {
				figureout.Value(s, &c.WebhookSecret, "value")
			})
			figureout.Group(s, "poll", func(s *figureout.Schema[gitLabConfig]) {
				figureout.Value(s, &c.PollInterval, "value")
			})
		})
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{
		"GITLAB_WEBHOOK_VALUE": "hunter2",
		"GITLAB_POLL_VALUE":    "15s",
	}))
	require.NoError(t, err)
	require.Equal(t, "hunter2", cfg.WebhookSecret)
	require.Equal(t, 15*time.Second, cfg.PollInterval)
}

func TestGroupEmptyName(t *testing.T) {
	_, err := figureout.Derive(func(c *gitLabConfig, s *figureout.Schema[gitLabConfig]) {
		figureout.Group(s, "", func(*figureout.Schema[gitLabConfig]) {})
		figureout.IgnorePath(s, "Token")
		figureout.IgnorePath(s, "WebhookEnabled")
		figureout.IgnorePath(s, "WebhookSecret")
		figureout.IgnorePath(s, "PollInterval")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty group name")
}
