# figureout [![](https://img.shields.io/badge/go-pkg-00ADD8)](https://pkg.go.dev/github.com/go-faster/figureout#section-documentation) [![](https://img.shields.io/codecov/c/github/go-faster/figureout?label=cover)](https://codecov.io/gh/go-faster/figureout) [![alpha](https://img.shields.io/badge/-alpha-orange)](https://go-faster.org/docs/projects/status#alpha)

Descriptor-driven configuration for Go: declare the configuration once, derive
decoding, validation, defaults, documentation and schemas from that one
declaration.

```console
go get github.com/go-faster/figureout
```

The core, three sources (JSON, YAML, environment variables) and one target
(JSON Schema), wired end to end.

```go
type Config struct {
	Server  Server
	Timeout figureout.OptionalOf[time.Duration]
	Level   LogLevel

	logger any
}

var ConfigDescriptor = figureout.MustDerive(
	func(c *Config, s *figureout.Schema[Config]) {
		figureout.Object(s, &c.Server, "server", ServerDescriptor)
		figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)
		figureout.Enum(s, &c.Level, "level").ApplyDefault(LogInfo)
		figureout.Ignore(s, &c.logger, figureout.Reason("runtime dependency"))
	},
)

cfg, report, err := ConfigDescriptor.Resolve(
	yaml.File("config.yaml"),
	env.Current(env.Prefix("APP_")),   // later sources win
)
schema, diags, err := jsonschema.Generate(ConfigDescriptor, jsonschema.Semantic())
```

## Examples

Runnable documentation lives in [`example_test.go`](example_test.go) — layering,
erasing, optional values, enums, unions, schema generation and completeness,
each with verified output.

[`examples/service`](examples/service) is a small program that puts them
together:

```console
go run ./examples/service                        # resolve and print provenance
APP_SERVER_LISTEN_PORT=9090 go run ./examples/service  # env wins over the file
APP_SERVER_TIMEOUT=null go run ./examples/service # erase a value from the file
go run ./examples/service -schema                # JSON Schema for JSON input
go run ./examples/service -paths                 # every path, type and default
```

```text
listening on 0.0.0.0:9090
request timeout 30s
storage s3 bucket=service-data region=eu-central-1
level=warn tags=[service production] limits=map[cpu:4 memory:8]

provenance:
  server.address       yaml server.address examples/service/config.yaml:8:3
  server.port          env APP_SERVER_LISTEN_PORT
  server.timeout       yaml server.timeout examples/service/config.yaml:10:3
```

## Deriving a descriptor

`Derive` compiles the whole model before returning, so every mistake in a
description — a mistyped registration, a duplicate name, an unregistered field —
surfaces at once, as diagnostics. `MustDerive` turns them into a panic carrying
the same list.

Which one to use is a question of *where the failure should appear*, and the
answer differs by program shape:

```go
// A library: a broken descriptor is a programming error, and a panic in init
// is the right way to report one. This is the idiom the examples use.
var ConfigDescriptor = figureout.MustDerive(describe)

// A service: derive once, on the path that can report an error and exit 1.
var descriptor = sync.OnceValues(func() (*figureout.Descriptor[Config], error) {
	return figureout.Derive(describe)
})

func Load(paths ...string) (Config, *figureout.Report, error) {
	d, err := descriptor()
	if err != nil {
		return Config{}, nil, errors.Wrap(err, "descriptor")
	}
	return d.Resolve(yaml.File(paths[0]), env.Current(env.Prefix("APP_")))
}
```

`sync.OnceValues` keeps the compile-once property of a package variable while
moving the failure into `main`, where it prints as a configuration error rather
than as a crash. For a descriptor with a few hundred registrations that
difference is worth the four extra lines; below that, the package variable is
fine either way.

## Packages

| Package | Contents |
| --- | --- |
| `figureout` | descriptor, builder, pointer binding, completeness, constraints, enums, unions, carriers, diagnostics, resolution |
| `figureout/source/json` | JSON source, with file:line:column provenance |
| `figureout/source/yaml` | YAML source, tag-aware, anchors resolved |
| `figureout/source/env` | environment variable source |
| `figureout/schema/jsonschema` | JSON Schema target |

## Sources

Every source decodes into a layer rather than writing into the struct; layers
merge in order, and validation runs once on the merged value. The report keeps
the origin of each value:

```go
cfg, report, err := ConfigDescriptor.Resolve(
	json.File("config.json"),
	yaml.File("config.yaml", yaml.Optional()),
	env.Current(env.Prefix("APP_")),
)

report.OriginOf("server.port")   // env APP_PORT
```

```text
port must be at most 65535
  value: 70000
  source: json config.json:4:13
```

JSON and YAML stay separate adapters, as the design requires, sharing only an
internal document tree and the text scalar parser that env and YAML both need.
The differences are the point:

| | JSON | YAML | env |
| --- | --- | --- | --- |
| `8080` vs `"8080"` | distinct; a string needs `json.Accepts(json.String())` | distinct by tag: `!!int` vs `!!str` | everything is text |
| null (erases) | `null` | `null` or `~` | opt-in `env.NullLiteral` |
| positions | line and column | line and column | variable name |
| anchors | — | resolved before binding | — |

Both name their fields with `Name`, `Alias` and `Skip`, and both accept
`DisallowUnknownFields()` to report members no field claims.

A name is **relative to the object that declares it**, so nesting composes and
a nested field can never silently claim a top-level field's variable:

```go
// inside ServerDescriptor, nested under "server"
figureout.Value(s, &c.Port, "port", env.Name("LISTEN_PORT"))
// reads APP_SERVER_LISTEN_PORT, not APP_LISTEN_PORT
```

For env, the whole derivation is pluggable — the default joins the segments
with underscores and upper-cases them:

```go
env.Current(env.Names(func(f *figureout.FieldModel, segments []string) []string {
	return []string{strings.ToUpper(strings.Join(segments, "__"))}
}))
```

Whatever the naming produces is still collision-checked, so a function that
flattens away a level is reported rather than silently binding two fields to
one variable.

## Nesting

A nested object is either its own descriptor or an inline description:

```go
figureout.Object(s, &c.Server, "server", ServerDescriptor)  // shared or exported

figureout.ObjectFunc(s, &c.Server, "server", func(c *Server, s *figureout.Schema[Server]) {
	figureout.Value(s, &c.Port, "port", env.Name("LISTEN_PORT")).InRange(1, 65535)
})
```

`ObjectFunc` runs `describe` against a nested `Schema` rooted at the field, so
pointer binding, completeness and name collisions are scoped to `Server`
exactly as a separate `Derive` would scope them — including `env.Name`, which
still replaces only that field's segment and reads `SERVER_LISTEN_PORT`. A
pointer that leaves the nested struct is a foreign-pointer diagnostic rather
than a silent binding.

Use `Object` for a descriptor several parents share or that you want to export,
and `ObjectFunc` for a section with exactly one parent — which is most of them.

**The configuration path is not welded to the Go nesting.** The shape that reads
well in a file and the shape consumers want in Go are rarely the same shape, and
a configuration that has been around a while has both mismatches. `Group` opens
a path level with no Go struct behind it:

```go
figureout.Group(s, "webhook", func(s *figureout.Schema[GitLab]) {
	figureout.Value(s, &c.WebhookEnabled, "enabled").ApplyDefault(false)
	figureout.Value(s, &c.WebhookSecret, "secret", figureout.Hidden())
})
```

```yaml
webhook:
  enabled: true
  secret: hunter2      # GITLAB_WEBHOOK_SECRET
```

Only the path nests. The fields still bind to the declaring struct, so
completeness and duplicate registration see exactly what they would have seen
without the group — registering the same field inside and outside one is still
a duplicate. A group contributes a segment everywhere a nested object would:
environment variable names, provenance paths and generated schemas.

The inverse mismatch — a nested Go struct that is flat in the file — needs no
function, because registering descendants already covers the parent:

```go
figureout.Value(s, &c.Database.DSN, "dsn")   // Config.Database.DSN, spelled "dsn"
```

## Presence

Presence is spelled by the registration function, and the value type is
inferred from the carrier. Constraints are then typed as the element, never as
the carrier:

```go
type Config struct {
	Port    int
	Timeout figureout.OptionalOf[time.Duration]   // missing | present
}

figureout.Value(s, &c.Port, "port").InRange(1, 65535)              // T = int
figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)  // T = time.Duration
```

The type carries the `Of` suffix so the plain name stays free for the
function. `Value` rejects a carrier field with a diagnostic naming the
function to use instead, so the two cannot be mixed up silently.

A plain field is required: a missing value is an error unless the field has an
applied default. Optionality lives in the Go type, never in a pointer.

There is **no nullable carrier**. Optional and nullable are orthogonal in a
schema language, where an external spec forces the split, but configuration
has no such spec — and layering gives null a more useful job. See
[Merging](#merging).

## Merging

Sources decode into layers; layers merge in order; validation runs once, on
the merged value. Each field decides how its layers combine:

```go
figureout.Value(s, &c.Args,   "args")               // replace, the default
figureout.Value(s, &c.Tags,   "tags").MergeAppend() // lists accumulate
figureout.Value(s, &c.Limits, "limits").MergeByKey()// maps merge per entry
```

```yaml
# base.yaml            # override.yaml        # result
tags: [a, b]           tags: [c]              tags: [a, b, c]
limits: {cpu: 1, mem: 8}  limits: {mem: 16}   limits: {cpu: 1, mem: 16}
args: [x]              args: [y]              args: [y]
```

**An explicit null erases.** A null in a later layer drops what earlier layers
set, so the field falls back to its default, or to missing:

```yaml
# base.yaml       # override.yaml     # result
timeout: 30s      timeout: null       timeout is missing again
level: debug      level: null         level falls back to its default
```

That is why there is no nullable carrier: null never reaches the Go value, so
no consumer of a resolved config has to handle a third state. `report.ErasedBy(path)`
names the layer that erased a value, just as `OriginOf` names the one that set
it. A required field with no default that gets erased is an error, which is
the intended cost.

Environment variables have no null, so the spelling is opt-in per field:

```go
figureout.Optional(s, &c.Timeout, "timeout", env.NullLiteral("null"))
// APP_TIMEOUT=null erases; without the declaration, "null" is just text
```

Because null is a directive rather than a value, it appears in a schema only
when that schema describes what a source accepts, and only where erasing
leaves something to fall back on — `jsonschema.ForSource(...)` emits it,
`jsonschema.Semantic()` never does.

## Durations and units

Unit-suffixed integer keys outlive the configurations that introduced them.
Moving `timeout_seconds` onto `time.Duration` normally changes what the key
accepts — `180` would have to become `"180s"` — which breaks every deployment
already running. `Unit` keeps the key and still resolves a `time.Duration`:

```go
figureout.Value(s, &c.Timeout, "timeout_seconds", figureout.Unit(time.Second)).
	AtLeast(time.Second).
	AtMost(10 * time.Minute)
```

```yaml
timeout_seconds: 180     # 180 * time.Second
timeout_seconds: "3m"    # still accepted
```

Constraints stay typed as `time.Duration`, so the bound reads `AtLeast(time.Second)`
rather than `AtLeast(1)`. The generated schema describes the canonical form —
`{"type": "integer", "minimum": 1, "description": "… In seconds."}` — because
that is what the key is actually written as. Durations without a unit stay
strings, with their defaults and bounds spelled the way a source accepts them.

Because the duration spelling keeps working, migrating away is two safe steps:
add the unit, then add the duration-spelled key and deprecate the old one.

## Cross-field invariants

Constraints are per field; real configurations are full of rules that are not.
`Invariant` gives them somewhere to live that keeps the provenance the
descriptor already has:

```go
figureout.Invariant(s, "proxy-exists", func(c *Config) error {
	for i, site := range c.Fetch.Sites {
		if _, ok := c.Proxies[site.Proxy]; !ok {
			return figureout.At(fmt.Sprintf("fetch.sites[%d].proxy", i)).
				Errorf("no proxy named %q is configured", site.Proxy)
		}
	}
	return nil
})
```

```text
fetch.sites[0].proxy: no proxy named "gitlab" is configured
  source: config.yaml:41:5
```

Invariants run last, on a configuration whose every field already resolved and
validated, so a violation is never a knock-on effect of an error already
reported. `At` attaches the paths a rule is about — an element path resolves to
its nearest field for provenance — and `errors.Join` reports several violations
as several diagnostics. A plain `error` works too, without a path.

A rule is a Go function, so no target can emit it; `model.Invariants()` lists
the names so generated documentation can say a rule exists that the schema does
not describe.

## Deprecating and moving a key

`Deprecated` is metadata, and metadata alone tells an operator nothing. Setting
a deprecated key now lands a `SeverityWarning` in `report.Diagnostics`, with the
origin that set it, so a binary can say "you are using a key that is going away"
and still start.

`MovedFrom` is the behavior a configuration needs while it is being reshaped:

```go
figureout.Group(s, "api", func(s *figureout.Schema[Config]) {
	figureout.Value(s, &c.HTTPAddr, "http_addr",
		figureout.MovedFrom("http_addr", "legacy.addr"))
})
```

- the old spelling still resolves, with a warning naming both paths
- setting **both** spellings is an error, not a precedence rule — two spellings
  in one configuration are two intentions, and silently picking one is the worst
  available answer. `report.OriginOf` answers "was this set?" correctly, so
  setting the new key explicitly to its default value alongside the old one is
  caught too
- the old path appears in generated schemas as a deprecated property, never as
  a second field; a level that no longer exists is rebuilt as a deprecated
  object, so old nesting keeps parsing

The path is relative to the declaring descriptor, so it can name a former level.
A former path that runs through a nested descriptor rather than a group is
reported at derivation, because that descriptor may be shared.

## Enum and OneOf

The two are separate concepts, and the API keeps them apart.

`Enum` is a **set of allowed values** for one type. Values come from the type
itself wherever possible, so a stringer derivative stays the single source of
truth:

```go
figureout.Enum(s, &c.Level, "level")                        // AllValues() iter.Seq[LogLevel]
figureout.EnumSlice(s, &c.Mode, "mode")                     // Values() []Mode
figureout.EnumFunc(s, &c.Kind, "kind", KindValues)          // enumer's package-level func
figureout.EnumValues(s, &c.Mode, "mode", []Mode{ModeFast})  // explicit
```

`Enum` and `EnumSlice` take the provider as a **constraint**, so a type without
values is a compile error, not a descriptor diagnostic. `EnumFunc` covers
generators that emit a package-level function rather than a method. An enum
carried by an `OptionalOf` uses the option form:
`figureout.Optional(s, &c.Level, "level", figureout.EnumOf[LogLevel]())`. An
ad-hoc value set with no provider is `.Enum(values...)` on the builder.

`OneOf` is a **type sum**: a tagged union selecting between alternative shapes.
A discriminator is required, so decoding failures name the tag rather than
reporting every variant's errors, and generation maps onto JSON Schema
`oneOf` + `const`. A union is why the tree sources parse a whole document
before binding: the tag has to be read before its siblings can be interpreted.

```go
figureout.OneOf(s, &c.Backend, "backend",
	figureout.Discriminator("type"),
	figureout.Variant("s3", &c.Backend.S3, S3Descriptor),
	figureout.Variant("local", &c.Backend.Local, LocalDescriptor),
)
```

Variant fields must be pointers: the non-nil pointer is what records the
selection. The tag is laid out inline, as a sibling of the variant's members,
which is what the emitted JSON Schema describes and what the env source does
with `BACKEND_TYPE` alongside `BACKEND_BUCKET`:

```yaml
backend:
  type: s3
  bucket: configs
```

## Library choices

**JSON uses `encoding/json`.** Its `Token` and `InputOffset` are exported, so
every node carries an exact offset and a diagnostic can say
`config.json:4:13`. `go-faster/jx` is faster, but its `offset()` is
unexported, so provenance would degrade to the property path.
`encoding/json/v2` is excluded by build constraints on the current toolchain,
and a library cannot ask its consumers to set `GOEXPERIMENT=jsonv2`.

**YAML uses `go-faster/yaml`.** `Node` carries `Line` and `Column`, resolves
tags, and exposes anchors. `yaml.v4` was considered but is not yet available
here.

## Design notes

Six decisions worth stating outright, because each rules out an approach that
looks reasonable from the outside.

**Presence selects the function; the type is inferred.** The obvious API is one
helper per semantic type (`Int`, `String`, `Duration`), doubled for optionals.
A single generic `Carrier[T]` constraint collapsing those pairs cannot be
written: Go forbids a bare type parameter as a union term. So the split runs
the other way — `Value` and `Optional` infer the element type from the
carrier, one function per presence rather than two per semantic type.

The trade is that a wrong semantic kind is a compilation diagnostic rather than
a compile error, since `Value[T]` accepts any `T`. In exchange, constraints are
typed as the element: `AtLeast(time.Second)` on an `OptionalOf[Duration]`, not
`AtLeast(any)`.

**`FieldOption` is not generic.** A `FieldOption[V]` would force every option call
site to spell its type argument, because Go cannot infer a type argument for a
nested call such as `env.Name("PORT")`. Options are untyped; value-typed
operations (`ApplyDefault`, `Check`) live on the fluent builder or on generic
top-level helpers where inference works from the argument, as in
`figureout.Check("even", func(v int) error { … })`.

**No nullable carrier.** Optionality and nullability are orthogonal in a schema
language, where an external spec forces the split. Configuration has no such
spec, and layering gives null a better job: an explicit null is far more useful
as an erase directive than as a value, and once it is one, nothing nullable
ever reaches the Go type.

**Model and carrier types are suffixed.** `Object` and `Variant` are
registration functions, so the model types are `FieldModel`, `ObjectModel` and
`VariantModel`. For the same
reason the carriers are `OptionalOf[T]` and `NullableOf[T]`, leaving `Optional`
and `Nullable` free as registration functions.

**Merge policies are per field.** Append applies to lists and by-key to maps; a
policy that does not fit the field's semantic kind is a compilation
diagnostic. Objects are not deep-merged: their leaves merge individually,
which is the same result without the surprise of a block that cannot be
replaced wholesale.

**Registering descendants covers the parent.** Registering `&c.Server.Port`
without registering `c.Server` is accepted, and completeness then checks `Server`'s remaining fields
individually. Registering both a parent and its descendants is rejected as a
duplicate.

## Not yet implemented

TOML and flag sources; CUE source and schema output; generated documentation;
code generation; optimized unsafe accessors.

## Development

```console
make test        # go test, then go test -race
make test_fast   # go test ./...
make coverage    # profile.out plus a per-function summary
make fuzz        # the JSON and text scalar parsers
make golden      # refresh golden files
make example     # run examples/service end to end
make lint fmt    # golangci-lint
```

## License

[MIT](LICENSE)
