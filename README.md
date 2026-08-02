# figureout

[![go reference](https://pkg.go.dev/badge/github.com/go-faster/figureout.svg)](https://pkg.go.dev/github.com/go-faster/figureout)

Descriptor-driven configuration for Go: declare the configuration once, derive
decoding, validation, defaults, documentation and schemas from that one
declaration.

```console
go get github.com/go-faster/figureout
```

This is a scaffold of the design in [`_ref/configuration-library-design.md`](_ref/configuration-library-design.md):
the core, three sources (JSON, YAML, environment variables) and one target
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

## Deviations from the design document

Six places where the document's API could not be written as spelled, or where
a different shape was clearly better.

**Presence selects the function; the type is inferred.** The document proposes
per-semantic-type helpers (`Int`, `String`, `OptionalDuration`, …). A single
generic `Carrier[T]` constraint that would collapse those pairs cannot be
written — Go forbids a bare type parameter as a union term — so the split runs
the other way: `Value`, `Optional` and `Nullable` infer the element type from
the carrier, and there is exactly one registration function per presence rather
than two per semantic type.

The trade is that a wrong semantic kind is a compilation diagnostic instead of
a compile error, since `Value[T]` accepts any `T`; the document's §2.3 example
`figureout.Int(s, &c.Host, "host")` no longer applies. In exchange, constraints
are typed as the element — `AtLeast(time.Second)` on an `OptionalOf[Duration]`,
not `AtLeast(any)`.

**`FieldOption` is not generic.** `FieldOption[V]` would force every option call
site to spell its type argument, because Go cannot infer a type argument for a
nested call such as `env.Name("PORT")`. Options are untyped; value-typed
operations (`ApplyDefault`, `Check`) live on the fluent builder or on generic
top-level helpers where inference works from the argument, as in
`figureout.Check("even", func(v int) error { … })`.

**No nullable carrier.** The document models optionality and nullability as
separate wrappers (§6.1, §6.2). Only `OptionalOf` survives: in a layered
configuration an explicit null is far more useful as an erase directive than
as a value, and once it is one, nothing nullable ever reaches the Go type.

**Model and carrier types are suffixed.** `Object` and `Variant` are
registration functions, so the model types are `FieldModel`, `ObjectModel` and
`VariantModel`, following the document's own `DescriptorModel`. For the same
reason the carriers are `OptionalOf[T]` and `NullableOf[T]`, leaving `Optional`
and `Nullable` free as registration functions.

**Merge policies are per field.** The document lists `MergeReplace`,
`MergeAppend` and `MergeByKey` (§15.2) without fixing their scope. Append
applies to lists and by-key to maps; a policy that does not fit the field's
semantic kind is a compilation diagnostic. Objects are not deep-merged: their
leaves merge individually, which is the same result without the surprise of a
block that cannot be replaced wholesale.

**Registering descendants covers the parent.** The document leaves this open
(§22.5). Registering `&c.Server.Port` without registering `c.Server` is
accepted, and completeness then checks `Server`'s remaining fields
individually. Registering both a parent and its descendants is rejected as a
duplicate.

## Not yet implemented

The design's later phases: TOML and flag sources; CUE source and schema
output; generated documentation; code generation; optimized unsafe accessors.

## Development

```console
go test ./...
go test ./source/env/ -run xxx -fuzz FuzzParse
go test ./source/json/ -run xxx -fuzz FuzzParse
go test ./schema/jsonschema/ -update   # refresh golden files
go run ./examples/service              # end-to-end check
golangci-lint fmt ./... && golangci-lint run ./...
```

## License

[MIT](LICENSE)
