# figureout

Descriptor-driven configuration library. One typed declaration derives
decoding, validation, defaults and schemas. Design: `_ref/configuration-library-design.md`.

## Commands

```console
go test ./...
go test ./schema/jsonschema/ -update            # refresh golden files
go test ./source/json/ -run xxx -fuzz FuzzParse # also ./source/env/
go run ./examples/service                       # end-to-end UX check
golangci-lint fmt ./... && golangci-lint run ./...
```

## Layout

- `figureout` — core. Format-neutral: it must never import a source or a target.
- `source/{json,yaml,env}` — decode into a `Layer`, never into the struct.
- `schema/jsonschema` — reads `Model`, emits.
- `internal/tree` — document model plus binder, shared by JSON and YAML.
- `internal/scalar` — text scalar parser, shared by env and YAML.

## Invariants

- **Null is a merge directive, not a value.** It erases what earlier layers set.
  There is no nullable carrier, and null never reaches the resolved Go value.
- **Presence picks the function**: `Value` for plain, `Optional` for
  `OptionalOf[T]`. Element type is inferred from the carrier; constraints are
  typed as the element.
- **`Enum` is a set of values, `OneOf` is a type sum.** Never conflate them.
  Union tags are laid out inline, as siblings of the variant's members.
- **Every exported field must be registered, delegated, covered or ignored.**
  Completeness failures are the point, not an inconvenience.
- Model types are suffixed (`FieldModel`, `ObjectModel`, `VariantModel`)
  because `Object` and `Variant` are registration functions.
- **Source names are relative to the declaring object.** `env.Name("LISTEN_PORT")`
  inside a nested descriptor reads `SERVER_LISTEN_PORT`, so nesting composes and
  the collision check sees the real variable. env derivation is pluggable via
  `env.Names`.

## Conventions

- Examples in `example_test.go` are runnable documentation; keep their output
  blocks accurate, they catch real regressions.
- Golden files go in their own commit, separate from the code that changed them.
- A new source implements `figureout.Source`; a tree format should reuse
  `internal/tree` rather than binding on its own.
