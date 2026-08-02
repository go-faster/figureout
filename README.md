# figureout

Descriptor-driven configuration for Go: declare the configuration once, derive
decoding, validation, defaults, documentation and schemas from that one
declaration.

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
| null | `null` | `null`, `~`, or empty | not representable |
| positions | line and column | line and column | variable name |
| anchors | — | resolved before binding | — |

Both name their fields with `Name`, `Alias` and `Skip`, and both accept
`DisallowUnknownFields()` to report members no field claims.

## Presence

Presence is spelled by the registration function, and the value type is
inferred from the carrier. Constraints are then typed as the element, never as
the carrier:

```go
type Config struct {
	Port    int
	Timeout figureout.OptionalOf[time.Duration]   // missing | present
	Grace   figureout.NullableOf[time.Duration]   // missing | null | present
}

figureout.Value(s, &c.Port, "port").InRange(1, 65535)          // T = int
figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second) // T = time.Duration
figureout.Nullable(s, &c.Grace, "grace")
```

The types carry the `Of` suffix so the plain names stay free for the
functions. `Value` rejects a carrier field with a diagnostic naming the
function to use instead, so the two cannot be mixed up silently.

A plain field is required: a missing value is an error unless the field has an
applied default. Optionality lives in the Go type, never in a pointer.

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

Four places where the document's API could not be written as spelled, or where
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

**Model and carrier types are suffixed.** `Object` and `Variant` are
registration functions, so the model types are `FieldModel`, `ObjectModel` and
`VariantModel`, following the document's own `DescriptorModel`. For the same
reason the carriers are `OptionalOf[T]` and `NullableOf[T]`, leaving `Optional`
and `Nullable` free as registration functions.

**Registering descendants covers the parent.** The document leaves this open
(§22.5). Registering `&c.Server.Port` without registering `c.Server` is
accepted, and completeness then checks `Server`'s remaining fields
individually. Registering both a parent and its descendants is rejected as a
duplicate.

## Not yet implemented

The design's later phases: TOML and flag sources; CUE source and schema
output; generated documentation; collection merge policies; code generation;
optimized unsafe accessors.

## Development

```console
go test ./...
go test ./source/env/ -run xxx -fuzz FuzzParse
go test ./source/json/ -run xxx -fuzz FuzzParse
go test ./schema/jsonschema/ -update   # refresh golden files
golangci-lint fmt ./... && golangci-lint run ./...
```
