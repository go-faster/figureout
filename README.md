# figureout

Descriptor-driven configuration for Go: declare the configuration once, derive
decoding, validation, defaults, documentation and schemas from that one
declaration.

This is a scaffold of the design in [`_ref/configuration-library-design.md`](_ref/configuration-library-design.md),
implemented as a walking skeleton: the core plus one source (environment
variables) and one target (JSON Schema), wired end to end.

```go
type Config struct {
	Server  Server
	Timeout figureout.Optional[time.Duration]
	Level   LogLevel

	logger any
}

var ConfigDescriptor = figureout.MustDerive(
	func(c *Config, s *figureout.Schema[Config]) {
		figureout.Object(s, &c.Server, "server", ServerDescriptor)
		figureout.Duration(s, &c.Timeout, "timeout").AtLeast(time.Second)
		figureout.Enum(s, &c.Level, "level").ApplyDefault(LogInfo)
		figureout.Ignore(s, &c.logger, figureout.Reason("runtime dependency"))
	},
)

cfg, report, err := ConfigDescriptor.Resolve(env.Current(env.Prefix("APP_")))
schema, diags, err := jsonschema.Generate(ConfigDescriptor, jsonschema.Semantic())
```

## Packages

| Package | Contents |
| --- | --- |
| `figureout` | descriptor, builder, pointer binding, completeness, constraints, enums, unions, carriers, diagnostics, resolution |
| `figureout/source/env` | environment variable source |
| `figureout/schema/jsonschema` | JSON Schema target |

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
generators that emit a package-level function rather than a method. For carriers
the helpers do not spell, such as `Optional[LogLevel]`, use the option form:
`figureout.Field(s, &c.Level, "level", figureout.EnumOf[LogLevel]())`.

`OneOf` is a **type sum**: a tagged union selecting between alternative shapes.
A discriminator is required, so decoding failures name the tag rather than
reporting every variant's errors, and generation maps onto JSON Schema
`oneOf` + `const`:

```go
figureout.OneOf(s, &c.Backend, "backend",
	figureout.Discriminator("type"),
	figureout.Variant("s3", &c.Backend.S3, S3Descriptor),
	figureout.Variant("local", &c.Backend.Local, LocalDescriptor),
)
```

Variant fields must be pointers: the non-nil pointer is what records the
selection.

## Deviations from the design document

Four places where the document's API could not be written as spelled, or where
a different shape was clearly better.

**`Carrier[T]` cannot be a single generic constraint.** The document proposes
`interface{ T | Optional[T] | Nullable[T] }`, but Go forbids a bare type
parameter as a union term. The intent — one helper name per semantic type,
accepting a plain value or a carrier — is preserved with concrete per-type
constraints (`DurationCarrier`, `IntCarrier`, …), which keeps full inference at
the call site. `OptionalDuration`-style duplicate helpers are therefore not
needed.

**`FieldOption` is not generic.** `FieldOption[V]` would force every option call
site to spell its type argument, because Go cannot infer a type argument for a
nested call such as `env.Name("PORT")`. Options are untyped; value-typed
operations (`ApplyDefault`, `Check`) live on the fluent builder or on generic
top-level helpers where inference works from the argument, as in
`figureout.Check("even", func(v int) error { … })`.

**Model types are suffixed.** `Field`, `Object` and `Variant` are registration
functions in the public API, so the model types are `FieldModel`, `ObjectModel`
and `VariantModel`, following the document's own `DescriptorModel`.

**Registering descendants covers the parent.** The document leaves this open
(§22.5). Registering `&c.Server.Port` without registering `c.Server` is
accepted, and completeness then checks `Server`'s remaining fields
individually. Registering both a parent and its descendants is rejected as a
duplicate.

## Not yet implemented

The design's later phases: YAML, TOML, JSON and flag sources; CUE; generated
documentation; collection merge policies; code generation; optimized unsafe
accessors. `Nullable` is modelled and materialized, but no current source
produces an explicit null, since environment variables cannot express one.

## Development

```console
go test ./...
go test ./source/env/ -run xxx -fuzz FuzzParse
go test ./schema/jsonschema/ -update   # refresh golden files
golangci-lint fmt ./... && golangci-lint run ./...
```
