# figureout

Descriptor-driven configuration library. One typed declaration derives
decoding, validation, defaults and schemas.

## Commands

```console
make test      # go test, then go test -race
make coverage  # profile.out plus a per-function summary
make golden    # refresh golden files
make docs      # refresh examples/service/CONFIG.md
make fuzz      # JSON and text scalar parsers
make example   # end-to-end UX check
make lint fmt  # golangci-lint
```

CI is `go-faster/x` reusable workflows in `.github/workflows/x.yml`
(test, cover, lint, commit, codeql). `cover.yml` calls `make coverage`, so
that target must keep producing `profile.out`.

## Layout

- `figureout` — core. Format-neutral: it must never import a source or a target.
- `source/{json,yaml,env}` — decode into a `Layer`, never into the struct.
- `schema/jsonschema` — reads `Model`, emits.
- `schema/docs` — reads `Model`, emits a Markdown reference. Names come from
  the source itself through `figureout.SourceNamer`, never re-derived here.
- `internal/tree` — document model plus binder, shared by JSON and YAML.
- `internal/scalar` — text scalar parser, shared by env and YAML.

## Invariants

- **Null is a merge directive, not a value.** It erases what earlier layers set.
  There is no nullable carrier, and null never reaches the resolved Go value.
- **Presence picks the function**: `Value` or `Explicit` for plain, `Optional`
  for `OptionalOf[T]`, `Object` / `ObjectFunc` for a section, `OptionalObject` /
  `OptionalObjectFunc` for one under either optional carrier. Element type is
  inferred from the carrier; constraints are typed as the element. Carriers do
  not stack.
- **A pointer is indirection, never presence**: `OptionalOf` alone says a value
  may be missing. `*C` is a required section resolution allocates;
  `OptionalOf[*C]` is an optional one. A pointer to a scalar and a `**T` are
  both refused. An adopted `*T` that meant absence is converted to a carrier —
  `OptionalOf` marshals as the value it holds, so serialization is unchanged.
- **An optional section is present or it is not.** A nesting source says so with
  a `Section` marker at the object's own path, a flat one by having provided a
  member. Absent means the carrier stays unset and nothing inside is demanded.
  `FieldModel.OptionalSection` is the single test for one — a `Group` is never
  one, since it has no Go field to be absent from.
- **The function says what absence means**: `Explicit` errors, `Value` resolves
  to the zero value, a `Value` collection to an empty one. `Explicit` is honored
  wherever a field can appear, a collection and a list element included. A
  fallback the field's own constraints reject is a compilation diagnostic, never
  a silent value.
- **`Enum` is a set of values, `OneOf` is a type sum.** Never conflate them.
  Union tags are laid out inline, as siblings of the variant's members.
- **Every exported field must be registered, delegated, covered or ignored.**
  Completeness failures are the point, not an inconvenience.
- **`Opaque` is the only hole in strictness, and it is spelled.** A passthrough
  carries a subtree verbatim and exempts it from `DisallowUnknownFields`, so
  `Reason` is mandatory. Nothing inside has names, constraints or a schema; the
  binder never descends, which is what makes the exemption structural rather
  than a check somebody remembered.
- **A configuration type may refer to itself, so the model is a graph.** The
  cycle is in the type graph alone: recursion reaches a descriptor only through
  a pointer, a slice or a map, each of which may be absent, so every value is a
  finite tree. A model is published before its `describe` runs, and a nested
  registration that re-enters an open one binds to it instead of descending —
  `FieldModel.Recursive` is that back-edge. Only the shallowest spelling of a
  field is indexed; deeper paths are walked. A recursion through a *required*
  section is an infinite value and a diagnostic. jsonschema emits `$defs`/`$ref`
  (the root as `#`), docs renders the object once and links back, and env and
  file stop at the cycle the way they already stop at a collection of objects.
- Model types are suffixed (`FieldModel`, `ObjectModel`, `VariantModel`)
  because `Object` and `Variant` are registration functions.
- **An empty input is absent.** An empty environment variable and a zero-length
  file are what tooling materializes for a value nobody supplied, so neither
  reaches a layer; `AllowEmpty()` opts out. Erasing has its own spelling.
- **`$schema` is accepted at the document root and bound to nothing.** It is an
  editor annotation, not configuration, so the binder never reports it as
  unknown and `schema/jsonschema` declares it next to the registered properties.
  A closed root that omitted it would reject the member that attaches the schema
  we generate. Root only; a registered `$schema` field wins.
- **Source names are relative to the declaring object.** `env.Name("LISTEN_PORT")`
  inside a nested descriptor reads `SERVER_LISTEN_PORT`, so nesting composes and
  the collision check sees the real variable. env derivation is pluggable via
  `env.Names`.

## Conventions

- Examples in `example_test.go` are runnable documentation; keep their output
  blocks accurate, they catch real regressions.
- Golden files go in their own commit, separate from the code that changed them.
- A new source implements `figureout.Source`; a tree format should reuse
  `internal/tree` rather than binding on its own. A source that can name what it
  reads also implements `figureout.SourceNamer`, so documentation quotes the
  names it actually accepts, prefix included.
- `examples/service/CONFIG.md` is generated. Refresh it with `make docs`; a test
  in `examples/service` fails when it is stale.
