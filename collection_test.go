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
	figureout.Value(s, &e.Name, "name").NonEmpty()
	figureout.Value(s, &e.URLPatterns, "url_patterns").ApplyDefault([]string{})
	figureout.Value(s, &e.MaxBytes, "max_bytes").ApplyDefault(int64(0))
}

func describeProxy(e *proxy, s *figureout.Schema[proxy]) {
	figureout.Value(s, &e.Addr, "addr").NonEmpty()
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

func TestListMissingIsRequired(t *testing.T) {
	_, _, err := crawlDescriptor(t, false).Resolve(yaml.Bytes([]byte("proxies: {}\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sites")
	require.Contains(t, err.Error(), figureout.CodeMissingDefinition)
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
