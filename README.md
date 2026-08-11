# figureout [![](https://img.shields.io/badge/go-pkg-00ADD8)](https://pkg.go.dev/github.com/go-faster/figureout#section-documentation) [![](https://img.shields.io/codecov/c/github/go-faster/figureout?label=cover)](https://codecov.io/gh/go-faster/figureout) [![alpha](https://img.shields.io/badge/-alpha-orange)](https://go-faster.org/docs/projects/status#alpha)

Descriptor-driven configuration for Go: declare the configuration once, derive
decoding, validation, defaults, documentation and schemas from that one
declaration.

```console
go get github.com/go-faster/figureout
```

The core, four sources (JSON, YAML, environment variables, mounted files) and
two targets (JSON Schema, Markdown reference), wired end to end.

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
reference, diags, err := docs.Generate(ConfigDescriptor,
	docs.ForSource(yaml.File("config.yaml")),
	docs.ForSource(env.Current(env.Prefix("APP_"))),
)
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
go run ./examples/service -docs                  # the Markdown reference below
go run ./examples/service -paths                 # every path, type and default
```

```text
listening on 0.0.0.0:9090
request timeout 30s
storage s3 bucket=service-data region=eu-central-1 prefix=""
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
| `figureout` | descriptor, builder, pointer binding, completeness, constraints, invariants, enums, unions, carriers, secrets, diagnostics, resolution |
| `figureout/source/json` | JSON source, with file:line:column provenance |
| `figureout/source/yaml` | YAML source, tag-aware, anchors resolved |
| `figureout/source/env` | environment variable source |
| `figureout/source/file` | one value per file, for mounted secrets |
| `figureout/schema/jsonschema` | JSON Schema target |
| `figureout/schema/docs` | Markdown reference target |

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
| empty | `""` is a value | `""` is a value | absent; `env.AllowEmpty()` opts out |
| positions | line and column | line and column | variable name |
| anchors | — | resolved before binding | — |

Both name their fields with `Name`, `Alias` and `Skip`, and both accept
`DisallowUnknownFields()` to report members no field claims.

**`$schema` at the document root is always accepted.** It names the schema that
describes the file, which is how an editor finds it, so `DisallowUnknownFields()`
lets it through and `schema/jsonschema` declares it alongside the registered
properties — otherwise the generated schema would reject the very member that
attaches it. It is accepted at the root only, and a descriptor that registers
`$schema` itself keeps its own declaration.

**An empty variable is absent, not an empty value.** Container tooling
materializes a variable whether or not an operator supplied one — the `:-` in
`APP_TOKEN: ${APP_TOKEN:-}` is what you write so compose does not warn — and a
`.env` template ships with `APP_TOKEN=` on purpose. Reading that as a value
would blank the credential a file layer set, on the deploy that adopts the env
layer, with nothing logged: as far as the resolver is concerned the operator
set the field. `source/file` treats a zero-length file the same way, because a
`Secret` key that exists but is blank mounts as exactly that. Both take
`AllowEmpty()` where the empty string is a value an operator picks on purpose:

```go
env.Current(env.Prefix("APP_"))                     // APP_TOKEN= is absent
env.Current(env.Prefix("APP_"), env.AllowEmpty())   // APP_TOKEN= is ""
file.Dir("/run/secrets")                            // a zero-length file is absent
```

Erasing keeps its own spelling, because erasing is a decision: `null` in a
document, or `env.NullLiteral` where a variable should carry it.

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
	figureout.Explicit(s, &c.Port, "port", env.Name("LISTEN_PORT")).InRange(1, 65535)
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
	figureout.Value(s, &c.WebhookEnabled, "enabled")
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

## Lists and maps of objects

The lists in a configuration file are the part an operator actually edits, and
describing their elements is what gives them names, defaults, constraints,
provenance and a schema:

```go
figureout.ListOf(s, &c.Sites, "sites", func(e *Site, s *figureout.Schema[Site]) {
	figureout.Explicit(s, &e.Name, "name").NonEmpty()
	figureout.Value(s, &e.MaxBytes, "max_bytes")
})

figureout.MapOf(s, &c.Proxies, "proxies", describeProxy).MergeByKey()
figureout.List(s, &c.Projects, "projects", ProjectDescriptor)   // shared elements
```

**Each element binds to a path of its own**, so nothing about collections is
special: merging, defaults, validation, `report.OriginOf` and null-erasure are
the same per-path machinery everything else uses.

```text
sites[0].max_bytes          an unkeyed list, by position
sites[name=docs].max_bytes  a list merged by key
proxies[gitlab].url         a map, by key
```

A list of structs that nobody described is a **derivation** error naming
`ListOf`, rather than a descriptor that compiles clean and fails at resolve
time. An absent collection resolves to an empty one — see [Presence](#presence)
— and `sites: null` erases it back to empty.

### Merging elements

Merging needs to know which element in a later layer is which in an earlier one,
and a list does not carry that. So the policy says where identity comes from:

| | Identity | A later layer can |
| --- | --- | --- |
| `MergeReplace` (default) | none needed | replace the whole list |
| `MergeAppend` | none needed | add elements |
| `MergeByKey("name")` | an element field | edit, and add |

```go
figureout.ListOf(s, &c.Sites, "sites", describeSite).MergeByKey("name")
```

```yaml
# base.yaml            # override.yaml        # result
sites:                 sites:                 sites:
  - name: docs           - name: docs           - name: docs
    max_bytes: 10            max_bytes: 20          max_bytes: 20
  - name: wiki                                  - name: wiki
    max_bytes: 10                                   max_bytes: 10
```

Fields merge individually, so a later layer changes only what it names.
Positions deliberately do **not** merge: `sites[0]` in two files is the same
element only by accident, and prepending one entry would otherwise re-target
every override silently.

Keying a list makes its key field mandatory, and repeating a key within one
layer is an error rather than last-wins. Base order is preserved and unseen keys
are appended, so a later layer cannot reorder — do not key a list whose order is
meaningful. A map already identifies its entries, so `MergeByKey()` there takes
no argument, and `gitlab: null` removes an entry.

Environment variables and mounted files cannot express a collection of objects
and simply skip the field. An index convention would be a second, worse way to
write the same configuration.

## Presence

Presence is spelled by the registration function, and the value type is
inferred from the carrier. Constraints are then typed as the element, never as
the carrier:

```go
type Config struct {
	Port    int
	Timeout figureout.OptionalOf[time.Duration]   // missing | present
}

figureout.Explicit(s, &c.Port, "port").InRange(1, 65535)           // T = int
figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)  // T = time.Duration
```

The type carries the `Of` suffix so the plain name stays free for the
function. `Value` and `Explicit` reject a carrier field with a diagnostic
naming the function to use instead, so the two cannot be mixed up silently.

**What absence means is the registration function, not a modifier.** A plain
field is one of two things, and the call site says which:

```go
figureout.Explicit(s, &c.Database.DSN, "dsn")   // absent is an error
figureout.Value(s, &c.Jira.URL, "url")          // absent is ""
figureout.Value(s, &c.Jira.MaxResults, "max")   // absent is 0
```

Most optional scalars have no meaningful default beyond the zero value, and
the zero value is already visible in the Go type; `Value` says so in one word
instead of a hundred repetitions of `ApplyDefault("")`. `Explicit` is for what
an operator has to decide — an address, a credential, a port.

**A fallback the field itself rejects never resolves silently.** `Value` on a
field constrained by `NonEmpty`, `InRange`, `Enum` or a `Check`, and any
collection constrained by `MinItems`, is a compilation diagnostic naming the
way out, because a value no source could have written is a descriptor that
cannot work:

```text
constraint.type_mismatch [patterns]: an empty list does not satisfy the field's
own constraints (length must be at least 1, got 0); register it with Explicit,
or mark it Required, so absence is an error
```

`Value(...).Required()` is `Explicit` spelled the long way, and `ApplyDefault`
replaces the fallback with a value of your own. Optionality itself lives in the
Go type, never in a pointer.

**A collection has a fallback of its own.** An absent list and an empty one are
the same statement about the world, so a list or map nobody configured resolves
to an empty one rather than to a diagnostic — and to an empty value, not a nil
one, so it encodes as `[]` rather than `null` one layer further out. `Explicit`
and `Required()` both opt back in where a section really must be declared:

```go
figureout.ListOf(s, &c.Sites, "sites", describeSite)              // absent is empty
figureout.ListOf(s, &c.Backends, "backends", describeBackend).Required()
figureout.Explicit(s, &c.Patterns, "patterns").MinItems(1)        // absent is an error
```

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

## Secrets

`Hidden` is documentation metadata and does nothing else, which leaves a token
one `Pattern` or `MinLength` failure away from a log. `Secret` has teeth:

```go
figureout.Explicit(s, &c.Token, "token", figureout.Secret()).Pattern(`^sk-[a-z0-9]+$`)
```

```text
token: must match "^sk-[a-z0-9]+$"     # never "value: hunter2"
  source: config.yaml:3:8
```

A secret's value never appears in a message the library formats — not in a
constraint failure, not in a decoding error from a source. `Secret` implies
`Hidden`, and JSON Schema marks the property `writeOnly`. `report.Secret(path)`
and `report.Secrets()` let a consumer walking `report.Origins()` apply the same
rule to its own logging.

**Where a secret comes from is a deliberate choice.** figureout owns the two
mechanisms an operator actually deploys, and neither is a value-level
indirection written into the configuration file:

| | |
| --- | --- |
| an environment variable | `env.Current(env.Prefix("APP_"))` binds `database.dsn` to `APP_DATABASE_DSN` directly |
| a mounted file | `file.Dir("/run/secrets")` reads `database.dsn` from a file of that name |

`source/file` is the shape a Kubernetes secret mount, a Docker secret and
systemd's `LoadCredential` all present: a directory whose entries are named
after the values they hold. One trailing newline is stripped, so a secret
written with `echo` reads back as written; a missing file leaves the field to
earlier layers. Names compose exactly as env's do, and `file.Names` replaces the
derivation wholesale.

An in-document `{value, env, file}` carrier is deliberately **not** provided.
Its `env:` half is redundant — the env source already binds the field directly,
which is strictly better than an indirection the file has to spell — and its
`file:` half is `source/file` with the mapping written out by hand. If a
configuration must keep that shape for compatibility, it is a `WithDecoder`
plus the shapes it accepts, not something the core owns.

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

A warning is data, not only prose. Where the deprecation is a move, the
diagnostic carries the superseding path in `MovedTo`, so an application that
phrases its own warnings never parses `Message`:

```go
for _, d := range report.Diagnostics {
	if d.Code == figureout.CodeDeprecated && d.MovedTo != "" {
		warnf("%s is deprecated; use %s instead (%s)", d.FieldPath, d.MovedTo, d.Origin)
	}
}
```

`CodeMovedConflict` carries it too, so "set one of X or Y" is expressible
without either path being a substring of a sentence. It is empty for a
`Deprecated` with no replacement.

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
- a former path covers the whole subtree beneath it, so a moved object hands
  over every member, and a moved `ScalarOr` field moves whichever spelling was
  used

A former path is a fact about **documents**. `env` and `file` derive their names
from the path, and `database_dsn` and `database.dsn` derive the same variable,
so a shadow would collide with its own target; both sources skip former paths
and read the field under its current name. Where a variable really did exist
under an old name, that is what `env.Alias` is for — an env-side fact,
independent of the file-side rename.

The path is relative to the declaring descriptor, so it can name a former level.
That scope is also what decides whether a former path is expressible at all:
`Group` above keeps the field declared by the root schema, so root-relative
`http_addr` is in scope. The same registration inside `ObjectFunc` is not — the
field belongs to the nested descriptor, where `http_addr` resolves to the field
itself, and derivation says so. A former path running through a nested
descriptor rather than a group is reported too, because that descriptor may be
shared.

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

## A scalar, or an object

`OneOf` cannot express "a scalar, or an object": a union needs a discriminator,
and a bare scalar has nowhere to put one. Written by hand it is a `WithDecoder`
plus `Shape` values that duplicate the descriptor already describing the same
thing — and drift the moment a field is added to it. `ScalarOr` derives both:

```go
figureout.ScalarOr(s, &c.AuthToken, "auth_token", SecretDescriptor,
	func(v string) Secret { return Secret{Value: v} })
```

```yaml
auth_token: sk-live-...          # widened by the function
auth_token: {file: /run/token}   # decoded by the descriptor
```

The accepted shapes come from the descriptor and from the scalar type, so they
cannot drift from what the binder accepts: JSON Schema emits `oneOf` over the
two, and a source with no object syntax — environment variables, mounted files —
takes the scalar at the object's own name (`AUTH_TOKEN`), while its members
still bind under it (`AUTH_TOKEN_FILE`).

The two spellings never half-merge across layers. A widened scalar stands for
the whole object, so whichever spelling a later layer uses replaces the other
outright.

## Reference documentation

`schema/docs` renders the compiled model as Markdown, so the documented
configuration cannot drift from the decoded one:

```go
reference, diags, err := docs.Generate(ConfigDescriptor,
	docs.Title("Service configuration"),
	docs.ForSource(yaml.File("config.yaml")),
	docs.ForSource(env.Current(env.Prefix("APP_"))),
)
```

One table per object, nested objects and collection elements as sections of
their own, a union rendered once per variant with the tag that selects it.
Hidden fields are omitted, deprecated ones are marked rather than dropped, and a
rule with no prose form — an opaque `Check` — is reported the way `jsonschema`
reports what it cannot represent.

`ForSource` takes a configured source rather than a `SourceID`, because the
names it documents belong to that configuration: `env.Prefix("APP_")` is what
makes the variable `APP_SERVER_LISTEN_PORT`. The source answers through
`figureout.SourceNamer`, so a column quotes the names actually read.

[`examples/service/CONFIG.md`](examples/service/CONFIG.md) is generated this
way, and a test fails when it falls behind the descriptor.

`Build` returns the [`Page`](schema/docs/page.go) that `Generate` renders, for a
caller that wants another format.

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
the other way — `Value`, `Explicit` and `Optional` infer the element type
from the carrier, one function per presence rather than two per semantic type.

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

TOML and flag sources; CUE source and schema output; code generation; optimized unsafe accessors. `ScalarOr` covers a field, not yet
a list element: `projects: [group/docs]` alongside `[{ref: group/docs}]` still
needs a decoder. Invariants are declared on the configuration that owns a list,
not on its elements.

## Development

```console
make test        # go test, then go test -race
make test_fast   # go test ./...
make coverage    # profile.out plus a per-function summary
make fuzz        # the JSON and text scalar parsers
make golden      # refresh golden files
make docs        # refresh examples/service/CONFIG.md
make example     # run examples/service end to end
make lint fmt    # golangci-lint
```

## License

[MIT](LICENSE)
