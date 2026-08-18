package figureout_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type site struct {
	Name        string
	URLPatterns []string
	MaxBytes    int64
}

type proxy struct {
	Addr string
	TLS  bool
}

type crawlConfig struct {
	Sites   []site
	Proxies map[string]proxy
}

func describeSite(e *site, s *figureout.Schema[site]) {
	figureout.Explicit(s, &e.Name, "name").NonEmpty()
	figureout.Value(s, &e.URLPatterns, "url_patterns").ApplyDefault([]string{})
	figureout.Value(s, &e.MaxBytes, "max_bytes").ApplyDefault(int64(0))
}

func describeProxy(e *proxy, s *figureout.Schema[proxy]) {
	figureout.Explicit(s, &e.Addr, "addr").NonEmpty()
	figureout.Value(s, &e.TLS, "tls").ApplyDefault(false)
}

func crawlDescriptor(t *testing.T, key bool) *figureout.Descriptor[crawlConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		list := figureout.ListOf(s, &c.Sites, "sites", describeSite)
		if key {
			list.MergeByKey("name")
		}
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy).MergeByKey()
	})
	require.NoError(t, err)
	return d
}

func TestListOfDecodesElements(t *testing.T) {
	cfg, report, err := crawlDescriptor(t, false).Resolve(yaml.Bytes([]byte(`
sites:
  - name: docs
    url_patterns: ["https://x/*"]
    max_bytes: 10
  - name: wiki
proxies: {}
`)))
	require.NoError(t, err)
	require.Equal(t, []site{
		{Name: "docs", URLPatterns: []string{"https://x/*"}, MaxBytes: 10},
		{Name: "wiki", URLPatterns: []string{}, MaxBytes: 0},
	}, cfg.Sites, "element defaults apply per element")

	origin, ok := report.OriginOf("sites[0].max_bytes")
	require.True(t, ok, "an element field has provenance of its own")
	require.Equal(t, 5, origin.Line)
	require.Equal(t, "sites[0].max_bytes", origin.Name)
}

func TestListOfValidatesElements(t *testing.T) {
	_, report, err := crawlDescriptor(t, false).Resolve(yaml.Bytes([]byte(`
sites:
  - name: ""
proxies: {}
`)))
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "sites[0].name", report.Diagnostics[0].FieldPath)
	require.Contains(t, report.Diagnostics[0].Message, "length must be at least 1")
}

func TestListReplaceIsTheDefault(t *testing.T) {
	cfg, _, err := crawlDescriptor(t, false).Resolve(
		yaml.Bytes([]byte("sites:\n  - name: docs\n  - name: wiki\nproxies: {}\n")),
		yaml.Bytes([]byte("sites:\n  - name: blog\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"blog"}, siteNames(cfg.Sites),
		"a layer that provides the list provides all of it")
}

func TestListAppend(t *testing.T) {
	d, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		figureout.ListOf(s, &c.Sites, "sites", describeSite).MergeAppend()
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy)
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(
		yaml.Bytes([]byte("sites:\n  - name: docs\nproxies: {}\n")),
		yaml.Bytes([]byte("sites:\n  - name: wiki\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"docs", "wiki"}, siteNames(cfg.Sites))
}

func TestListMergeByKeyEditsOneElement(t *testing.T) {
	cfg, report, err := crawlDescriptor(t, true).Resolve(
		yaml.Bytes([]byte(`
sites:
  - name: docs
    url_patterns: ["https://x/*"]
    max_bytes: 10
  - name: wiki
    max_bytes: 10
proxies: {}
`)),
		yaml.Bytes([]byte("sites:\n  - name: docs\n    max_bytes: 20\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []site{
		{Name: "docs", URLPatterns: []string{"https://x/*"}, MaxBytes: 20},
		{Name: "wiki", URLPatterns: []string{}, MaxBytes: 10},
	}, cfg.Sites, "fields merge individually, and base order is preserved")

	origin, ok := report.OriginOf("sites[name=docs].max_bytes")
	require.True(t, ok)
	require.Equal(t, 3, origin.Line, "the later layer set it")

	origin, ok = report.OriginOf("sites[name=docs].url_patterns")
	require.True(t, ok)
	require.Equal(t, 4, origin.Line, "and the base layer still owns the rest")
}

func TestListMergeByKeyAppendsUnseenKeys(t *testing.T) {
	cfg, _, err := crawlDescriptor(t, true).Resolve(
		yaml.Bytes([]byte("sites:\n  - name: docs\nproxies: {}\n")),
		yaml.Bytes([]byte("sites:\n  - name: blog\n  - name: docs\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"docs", "blog"}, siteNames(cfg.Sites),
		"base order wins; a later layer adds but cannot reorder")
}

func TestListMergeByKeyIsPositionIndependent(t *testing.T) {
	// Prepending an element in the base layer does not re-target the override,
	// which is the whole reason a key beats a position.
	cfg, _, err := crawlDescriptor(t, true).Resolve(
		yaml.Bytes([]byte("sites:\n  - name: blog\n  - name: docs\n    max_bytes: 1\nproxies: {}\n")),
		yaml.Bytes([]byte("sites:\n  - name: docs\n    max_bytes: 20\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []int64{0, 20}, siteBytes(cfg.Sites))
}

func TestListMergeByKeyRequiresTheKey(t *testing.T) {
	_, _, err := crawlDescriptor(t, true).Resolve(yaml.Bytes([]byte(`
sites:
  - max_bytes: 10
proxies: {}
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), `must set "name", which identifies an element`)
}

func TestListMergeByKeyRejectsDuplicatesInOneLayer(t *testing.T) {
	_, _, err := crawlDescriptor(t, true).Resolve(yaml.Bytes([]byte(`
sites:
  - name: docs
  - name: docs
proxies: {}
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "repeats")
	require.Contains(t, err.Error(), figureout.CodeDuplicateName)
}

func TestListUnkeyedDoesNotMergeByPosition(t *testing.T) {
	// Without a key an element has only its position, which belongs to the
	// document it came from: replace is the honest answer.
	cfg, _, err := crawlDescriptor(t, false).Resolve(
		yaml.Bytes([]byte("sites:\n  - name: docs\n    max_bytes: 10\nproxies: {}\n")),
		yaml.Bytes([]byte("sites:\n  - name: blog\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []site{{Name: "blog", URLPatterns: []string{}}}, cfg.Sites)
}

func TestListEmptyIsNotMissing(t *testing.T) {
	cfg, _, err := crawlDescriptor(t, false).Resolve(yaml.Bytes([]byte("sites: []\nproxies: {}\n")))
	require.NoError(t, err)
	require.NotNil(t, cfg.Sites)
	require.Empty(t, cfg.Sites)
}

func TestAbsentCollectionIsEmpty(t *testing.T) {
	// "No sites are configured" and "the sites list is empty" are the same
	// statement about the world, so a section nobody wrote is not an error.
	cfg, _, err := crawlDescriptor(t, false).Resolve(yaml.Bytes([]byte("{}\n")))
	require.NoError(t, err)
	require.NotNil(t, cfg.Sites, "empty rather than nil, so it encodes as [] and not null")
	require.Empty(t, cfg.Sites)
	require.NotNil(t, cfg.Proxies)
	require.Empty(t, cfg.Proxies)
}

func TestAbsentPlainCollectionIsEmpty(t *testing.T) {
	// The same rule for a list that was never described.
	type c struct {
		Tags   []string
		Limits map[string]int
	}
	d, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Value(s, &cfg.Tags, "tags")
		figureout.Value(s, &cfg.Limits, "limits")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("{}\n")))
	require.NoError(t, err)
	require.NotNil(t, cfg.Tags)
	require.Empty(t, cfg.Tags)
	require.NotNil(t, cfg.Limits)
	require.Empty(t, cfg.Limits)
}

func TestRequiredCollectionOptsBackIn(t *testing.T) {
	d, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		figureout.ListOf(s, &c.Sites, "sites", describeSite).Required()
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy)
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("{}\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sites")
	require.Contains(t, err.Error(), figureout.CodeMissingDefinition)
}

func TestRequiredRejectsAnOptionalCarrier(t *testing.T) {
	type c struct {
		Tags figureout.OptionalOf[[]string]
	}
	_, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Optional(s, &cfg.Tags, "tags").Required()
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be required")
}

func TestErasedCollectionIsEmpty(t *testing.T) {
	cfg, report, err := crawlDescriptor(t, false).Resolve(
		yaml.Bytes([]byte("sites:\n  - name: docs\nproxies: {}\n")),
		yaml.Bytes([]byte("sites: null\n")),
	)
	require.NoError(t, err)
	require.Empty(t, cfg.Sites, "an erase falls back the same way an absence does")

	_, erased := report.ErasedBy("sites")
	require.True(t, erased)
}

func TestCollectionSchemaRequired(t *testing.T) {
	raw, _, err := jsonschema.Generate(crawlDescriptor(t, false), jsonschema.Semantic())
	require.NoError(t, err)

	var doc struct {
		Required []string `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotContains(t, doc.Required, "sites",
		"a collection that resolves to empty is not required of a document")
	require.NotContains(t, doc.Required, "proxies")
}

func TestMapOfDecodesEntries(t *testing.T) {
	cfg, report, err := crawlDescriptor(t, false).Resolve(yaml.Bytes([]byte(`
sites: []
proxies:
  gitlab:
    addr: "http://gitlab"
    tls: true
  github:
    addr: "http://github"
`)))
	require.NoError(t, err)
	require.Equal(t, map[string]proxy{
		"gitlab": {Addr: "http://gitlab", TLS: true},
		"github": {Addr: "http://github"},
	}, cfg.Proxies)

	origin, ok := report.OriginOf("proxies[gitlab].addr")
	require.True(t, ok)
	require.Equal(t, 5, origin.Line)
}

func TestMapOfReplaceIsTheDefault(t *testing.T) {
	d, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		figureout.ListOf(s, &c.Sites, "sites", describeSite)
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy)
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(
		yaml.Bytes([]byte("sites: []\nproxies:\n  gitlab:\n    addr: a\n  github:\n    addr: b\n")),
		yaml.Bytes([]byte("proxies:\n  gitlab:\n    addr: c\n")),
	)
	require.NoError(t, err)
	require.Equal(t, map[string]proxy{"gitlab": {Addr: "c"}}, cfg.Proxies)
}

func TestMapOfMergesPerEntry(t *testing.T) {
	// A map entry identifies itself, so MergeByKey lets a later layer edit one
	// entry, and only the fields it names within it.
	cfg, _, err := crawlDescriptor(t, false).Resolve(
		yaml.Bytes([]byte("sites: []\nproxies:\n  gitlab:\n    addr: a\n    tls: true\n  github:\n    addr: b\n")),
		yaml.Bytes([]byte("proxies:\n  gitlab:\n    addr: c\n")),
	)
	require.NoError(t, err)
	require.Equal(t, map[string]proxy{
		"gitlab": {Addr: "c", TLS: true},
		"github": {Addr: "b"},
	}, cfg.Proxies)
}

func TestMapOfEntryNullRemovesIt(t *testing.T) {
	cfg, report, err := crawlDescriptor(t, false).Resolve(
		yaml.Bytes([]byte("sites: []\nproxies:\n  gitlab:\n    addr: a\n  github:\n    addr: b\n")),
		yaml.Bytes([]byte("proxies:\n  gitlab: null\n")),
	)
	require.NoError(t, err)
	require.Equal(t, map[string]proxy{"github": {Addr: "b"}}, cfg.Proxies)

	_, erased := report.ErasedBy("proxies[gitlab]")
	require.True(t, erased, "null erases an entry, as it erases anything else")
}

func TestCollectionSkippedByEnv(t *testing.T) {
	// Environment variables cannot express a list of objects, and an index
	// convention would be a second, worse way to write the same configuration.
	cfg, _, err := crawlDescriptor(t, false).Resolve(
		yaml.Bytes([]byte("sites:\n  - name: docs\nproxies: {}\n")),
		env.Values(map[string]string{"SITES": "nonsense", "PROXIES": "nonsense"}),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"docs"}, siteNames(cfg.Sites))
}

func TestCollectionInvariantResolvesElementPaths(t *testing.T) {
	d, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		figureout.ListOf(s, &c.Sites, "sites", describeSite)
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy)
		figureout.Invariant(s, "patterns-required", func(c *crawlConfig) error {
			for i, site := range c.Sites {
				if len(site.URLPatterns) == 0 {
					return figureout.At(figureout.ElementPath("sites", strconv.Itoa(i))+".url_patterns").
						Errorf("site %q has no URL patterns", site.Name)
				}
			}
			return nil
		})
	})
	require.NoError(t, err)

	_, report, err := d.Resolve(yaml.Bytes([]byte("sites:\n  - name: docs\nproxies: {}\n")))
	require.Error(t, err)
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, "sites[0].url_patterns", report.Diagnostics[0].FieldPath)
	require.NotNil(t, report.Diagnostics[0].Origin,
		"an element path now resolves to a field rather than to its nearest enclosing one")
}

func TestUndescribedElementsFailAtDerive(t *testing.T) {
	_, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		figureout.Value(s, &c.Sites, "sites")
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "objects with no description")
	require.Contains(t, err.Error(), "ListOf or List")
}

func TestMergeKeyMustNameAnElementField(t *testing.T) {
	_, err := figureout.Derive(func(c *crawlConfig, s *figureout.Schema[crawlConfig]) {
		figureout.ListOf(s, &c.Sites, "sites", describeSite).MergeByKey("nope")
		figureout.MapOf(s, &c.Proxies, "proxies", describeProxy)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `merge key "nope" is not a field of the elements`)
}

func TestCollectionSchema(t *testing.T) {
	raw, _, err := jsonschema.Generate(crawlDescriptor(t, true), jsonschema.Semantic())
	require.NoError(t, err)

	var doc struct {
		Properties struct {
			Sites struct {
				Type  string         `json:"type"`
				Items map[string]any `json:"items"`
			} `json:"sites"`
			Proxies struct {
				AdditionalProperties map[string]any `json:"additionalProperties"`
			} `json:"proxies"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	require.Equal(t, "array", doc.Properties.Sites.Type)
	require.Equal(t, "object", doc.Properties.Sites.Items["type"])
	require.Contains(t, doc.Properties.Sites.Items["properties"], "max_bytes")
	require.Contains(t, doc.Properties.Proxies.AdditionalProperties["properties"], "addr")
}

func siteNames(sites []site) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.Name)
	}
	return out
}

func siteBytes(sites []site) []int64 {
	out := make([]int64, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.MaxBytes)
	}
	return out
}

type authToken struct {
	Token string
}

type authenticator struct {
	Type   string
	Tokens []authToken
	Limits map[string]authToken
}

type authConfig struct {
	Auth []authenticator
}

func authDescriptor(t *testing.T) *figureout.Descriptor[authConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *authConfig, s *figureout.Schema[authConfig]) {
		figureout.ListOf(s, &c.Auth, "auth", func(e *authenticator, s *figureout.Schema[authenticator]) {
			figureout.Value(s, &e.Type, "type")
			figureout.ListOf(s, &e.Tokens, "tokens", func(e *authToken, s *figureout.Schema[authToken]) {
				figureout.Value(s, &e.Token, "token")
			})
			figureout.MapOf(s, &e.Limits, "limits", func(e *authToken, s *figureout.Schema[authToken]) {
				figureout.Value(s, &e.Token, "token")
			})
		})
	})
	require.NoError(t, err)
	return d
}

// TestElementWithNoMembers pins that an element resolving entirely to defaults is still an
// element. Its members produce no assignments, so nothing but the element itself can claim its
// slot, and a list that quietly loses an entry is indistinguishable from one that never had it.
func TestElementWithNoMembers(t *testing.T) {
	cfg, _, err := authDescriptor(t).Resolve(yaml.Bytes([]byte("auth:\n  - {}\n  - type: bearer\n  - {}\n")))
	require.NoError(t, err)
	require.Equal(t, []authenticator{
		{Tokens: []authToken{}, Limits: map[string]authToken{}},
		{Type: "bearer", Tokens: []authToken{}, Limits: map[string]authToken{}},
		{Tokens: []authToken{}, Limits: map[string]authToken{}},
	}, cfg.Auth)
}

func TestMapEntryWithNoMembers(t *testing.T) {
	type entries struct {
		Limits map[string]authToken
	}
	d, err := figureout.Derive(func(c *entries, s *figureout.Schema[entries]) {
		figureout.MapOf(s, &c.Limits, "limits", func(e *authToken, s *figureout.Schema[authToken]) {
			figureout.Value(s, &e.Token, "token")
		})
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("limits:\n  soft: {}\n")))
	require.NoError(t, err)
	require.Equal(t, map[string]authToken{"soft": {}}, cfg.Limits)
}

func TestCollectionNestedInElement(t *testing.T) {
	cfg, _, err := authDescriptor(t).Resolve(yaml.Bytes([]byte(`
auth:
  - type: bearer
    tokens:
      - token: secret
      - token: other
    limits:
      soft: {token: x}
`)))
	require.NoError(t, err)
	require.Equal(t, []authenticator{{
		Type:   "bearer",
		Tokens: []authToken{{Token: "secret"}, {Token: "other"}},
		Limits: map[string]authToken{"soft": {Token: "x"}},
	}}, cfg.Auth, "a collection nested in an element resolves like any other")
}

func TestCollectionNestedInElementReplaces(t *testing.T) {
	cfg, _, err := authDescriptor(t).Resolve(
		yaml.Bytes([]byte("auth:\n  - type: bearer\n    tokens: [{token: a}, {token: b}]\n")),
		yaml.Bytes([]byte("auth:\n  - type: bearer\n    tokens: [{token: c}]\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []authToken{{Token: "c"}}, cfg.Auth[0].Tokens)
}

func TestCollectionNestedInElementErased(t *testing.T) {
	cfg, _, err := authDescriptor(t).Resolve(
		yaml.Bytes([]byte("auth:\n  - type: bearer\n    tokens: [{token: a}]\n")),
		yaml.Bytes([]byte("auth:\n  - type: bearer\n    tokens: null\n")),
	)
	require.NoError(t, err)
	require.Empty(t, cfg.Auth[0].Tokens)
}
