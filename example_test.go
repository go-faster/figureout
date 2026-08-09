package figureout_test

import (
	"fmt"
	"time"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

type quickstart struct {
	Address string
	Port    int
	Debug   bool
}

// Declare the configuration once, then resolve it from any source.
func Example() {
	descriptor := figureout.MustDerive(func(c *quickstart, s *figureout.Schema[quickstart]) {
		figureout.Explicit(s, &c.Address, "address").NonEmpty()
		figureout.Value(s, &c.Port, "port").InRange(1, 65535).ApplyDefault(8080)
		figureout.Value(s, &c.Debug, "debug").ApplyDefault(false)
	})

	cfg, _, err := descriptor.Resolve(
		yaml.Bytes([]byte("address: 0.0.0.0\ndebug: true\n")),
	)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%s:%d debug=%v\n", cfg.Address, cfg.Port, cfg.Debug)
	// Output: 0.0.0.0:8080 debug=true
}

type layered struct {
	Address string
	Port    int
	Tags    []string
}

// Sources merge in order, and the report says where each value came from.
func Example_layering() {
	descriptor := figureout.MustDerive(func(c *layered, s *figureout.Schema[layered]) {
		figureout.Value(s, &c.Address, "address")
		figureout.Value(s, &c.Port, "port")
		figureout.Value(s, &c.Tags, "tags").MergeAppend().ApplyDefault([]string{})
	})

	cfg, report, err := descriptor.Resolve(
		yaml.Bytes([]byte("address: 127.0.0.1\nport: 80\ntags: [base]\n")),
		env.Values(map[string]string{"APP_PORT": "9090", "APP_TAGS": "extra"}, env.Prefix("APP_")),
	)
	if err != nil {
		panic(err)
	}

	address, _ := report.OriginOf("address")
	port, _ := report.OriginOf("port")

	fmt.Printf("address=%s from %s\n", cfg.Address, address.Source)
	fmt.Printf("port=%d from %s %s\n", cfg.Port, port.Source, port.Name)
	fmt.Printf("tags=%v\n", cfg.Tags)
	// Output:
	// address=127.0.0.1 from yaml
	// port=9090 from env APP_PORT
	// tags=[base extra]
}

type erasable struct {
	Level   string
	Timeout figureout.OptionalOf[time.Duration]
}

// An explicit null in a later layer erases what earlier layers set.
func Example_erasing() {
	descriptor := figureout.MustDerive(func(c *erasable, s *figureout.Schema[erasable]) {
		figureout.Value(s, &c.Level, "level").ApplyDefault("info")
		figureout.Optional(s, &c.Timeout, "timeout")
	})

	cfg, report, err := descriptor.Resolve(
		yaml.Bytes([]byte("level: debug\ntimeout: 30s\n")),
		yaml.Bytes([]byte("level: null\ntimeout: null\n")),
	)
	if err != nil {
		panic(err)
	}

	erasedBy, _ := report.ErasedBy("level")

	fmt.Printf("level=%s (erased by %s, so the default applies)\n", cfg.Level, erasedBy.Source)
	fmt.Printf("timeout set=%v\n", cfg.Timeout.IsSet())
	// Output:
	// level=info (erased by yaml, so the default applies)
	// timeout set=false
}

type optionalConfig struct {
	Timeout figureout.OptionalOf[time.Duration]
}

// Optional distinguishes "no source provided it" from "provided as zero".
func Example_optional() {
	descriptor := figureout.MustDerive(func(c *optionalConfig, s *figureout.Schema[optionalConfig]) {
		figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)
	})

	for _, document := range []string{"{}", `{"timeout": "1m"}`} {
		cfg, _, err := descriptor.Resolve(json.Bytes([]byte(document)))
		if err != nil {
			panic(err)
		}
		fmt.Printf("%-20s -> %v\n", document, cfg.Timeout)
	}
	// Output:
	// {}                   -> none
	// {"timeout": "1m"}    -> some(1m0s)
}

type leveled struct {
	Level LogLevel
}

// An enum takes its values from the type, so a stringer derivative stays the
// single source of truth.
func Example_enum() {
	descriptor := figureout.MustDerive(func(c *leveled, s *figureout.Schema[leveled]) {
		figureout.Enum(s, &c.Level, "level").ApplyDefault(LogInfo)
	})

	cfg, _, err := descriptor.Resolve(env.Values(map[string]string{"LEVEL": "warn"}))
	if err != nil {
		panic(err)
	}
	fmt.Println("level:", cfg.Level)

	_, _, err = descriptor.Resolve(env.Values(map[string]string{"LEVEL": "verbose"}))
	fmt.Println("bad value:", err)
	// Output:
	// level: warn
	// bad value: constraint.type_mismatch [level]: must be one of [debug, info, warn, error], got verbose (env LEVEL)
}

type storage struct {
	Backend backend
}

type backend struct {
	S3    *s3Backend
	Local *localBackend
}

type s3Backend struct{ Bucket string }

type localBackend struct{ Path string }

// A union selects between alternative shapes, tagged by a discriminator.
func Example_oneOf() {
	s3 := figureout.MustDerive(func(b *s3Backend, s *figureout.Schema[s3Backend]) {
		figureout.Explicit(s, &b.Bucket, "bucket").NonEmpty()
	})
	local := figureout.MustDerive(func(b *localBackend, s *figureout.Schema[localBackend]) {
		figureout.Explicit(s, &b.Path, "path").NonEmpty()
	})

	descriptor := figureout.MustDerive(func(c *storage, s *figureout.Schema[storage]) {
		figureout.OneOf(s, &c.Backend, "backend",
			figureout.Discriminator("type"),
			figureout.Variant("s3", &c.Backend.S3, s3),
			figureout.Variant("local", &c.Backend.Local, local),
		)
	})

	cfg, _, err := descriptor.Resolve(yaml.Bytes([]byte("backend:\n  type: s3\n  bucket: configs\n")))
	if err != nil {
		panic(err)
	}
	fmt.Println("bucket:", cfg.Backend.S3.Bucket)
	fmt.Println("local selected:", cfg.Backend.Local != nil)

	_, _, err = descriptor.Resolve(yaml.Bytes([]byte("backend:\n  type: gcs\n")))
	fmt.Println("bad tag:", err)
	// Output:
	// bucket: configs
	// local selected: false
	// bad tag: union.invalid [backend.type]: unknown variant "gcs", want one of [s3, local] (yaml backend.type)
}

type documented struct {
	Address string
	Port    int
}

// The same descriptor generates a JSON Schema.
func Example_jsonSchema() {
	descriptor := figureout.MustDerive(func(c *documented, s *figureout.Schema[documented]) {
		figureout.Explicit(s, &c.Address, "address").Doc("Listen address.").NonEmpty()
		figureout.Explicit(s, &c.Port, "port").InRange(1, 65535)
	})

	schema, _, err := jsonschema.Generate(descriptor, jsonschema.Semantic())
	if err != nil {
		panic(err)
	}
	fmt.Println(string(schema))
	// Output:
	// {
	//   "$schema": "https://json-schema.org/draft/2020-12/schema",
	//   "additionalProperties": false,
	//   "properties": {
	//     "address": {
	//       "description": "Listen address.",
	//       "minLength": 1,
	//       "type": "string"
	//     },
	//     "port": {
	//       "maximum": 65535,
	//       "minimum": 1,
	//       "type": "integer"
	//     }
	//   },
	//   "required": [
	//     "address",
	//     "port"
	//   ],
	//   "type": "object"
	// }
}

type incomplete struct {
	Address string
	Port    int
}

// Every exported field must be registered or explicitly ignored, so a struct
// and its description cannot drift apart.
func Example_completeness() {
	_, err := figureout.Derive(func(c *incomplete, s *figureout.Schema[incomplete]) {
		figureout.Value(s, &c.Address, "address")
	})
	fmt.Println(err)
	// Output: field.missing_definition [incomplete.Port]: incomplete.Port is neither registered nor explicitly ignored
}
