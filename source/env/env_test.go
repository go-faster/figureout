package env

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
)

func TestDerive(t *testing.T) {
	for _, tt := range []struct {
		path string
		want string
	}{
		{"port", "PORT"},
		{"server.port", "SERVER_PORT"},
		{"server.listenPort", "SERVER_LISTEN_PORT"},
		{"log-level", "LOG_LEVEL"},
		{"a.b.c", "A_B_C"},
	} {
		require.Equal(t, tt.want, derive(tt.path), tt.path)
	}
}

func typeOf[T any](kind figureout.TypeKind) figureout.Type {
	return figureout.Type{Kind: kind, Go: reflect.TypeFor[T]()}
}

func listOf[T any](elem figureout.Type) figureout.Type {
	return figureout.Type{Kind: figureout.TypeList, Go: reflect.TypeFor[[]T](), Elem: &elem}
}

type Port uint16

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		name    string
		typ     figureout.Type
		raw     string
		want    any
		wantErr bool
	}{
		{name: "bool", typ: typeOf[bool](figureout.TypeBoolean), raw: "true", want: true},
		{name: "bool invalid", typ: typeOf[bool](figureout.TypeBoolean), raw: "yes!", wantErr: true},
		{name: "int", typ: typeOf[int](figureout.TypeInteger), raw: "42", want: 42},
		{name: "int negative", typ: typeOf[int](figureout.TypeInteger), raw: "-1", want: -1},
		{name: "named uint", typ: typeOf[Port](figureout.TypeInteger), raw: "8080", want: Port(8080)},
		{name: "uint overflow", typ: typeOf[Port](figureout.TypeInteger), raw: "70000", wantErr: true},
		{name: "uint negative", typ: typeOf[Port](figureout.TypeInteger), raw: "-1", wantErr: true},
		{name: "float", typ: typeOf[float64](figureout.TypeNumber), raw: "1.5", want: 1.5},
		{name: "string", typ: typeOf[string](figureout.TypeString), raw: "hello", want: "hello"},
		{name: "string empty", typ: typeOf[string](figureout.TypeString), raw: "", want: ""},
		{name: "duration", typ: typeOf[time.Duration](figureout.TypeDuration), raw: "1m30s", want: 90 * time.Second},
		{name: "duration invalid", typ: typeOf[time.Duration](figureout.TypeDuration), raw: "1 minute", wantErr: true},
		{
			name: "timestamp",
			typ:  typeOf[time.Time](figureout.TypeTimestamp),
			raw:  "2026-08-02T10:00:00Z",
			want: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
		},
		{name: "timestamp invalid", typ: typeOf[time.Time](figureout.TypeTimestamp), raw: "yesterday", wantErr: true},
		{name: "bytes", typ: typeOf[[]byte](figureout.TypeBytes), raw: "aGk=", want: []byte("hi")},
		{name: "bytes invalid", typ: typeOf[[]byte](figureout.TypeBytes), raw: "!!", wantErr: true},
		{
			name: "list",
			typ:  listOf[string](typeOf[string](figureout.TypeString)),
			raw:  "a,b,c",
			want: []string{"a", "b", "c"},
		},
		{
			name: "list trims elements",
			typ:  listOf[int](typeOf[int](figureout.TypeInteger)),
			raw:  "1, 2 ,3",
			want: []int{1, 2, 3},
		},
		{
			name: "list empty",
			typ:  listOf[string](typeOf[string](figureout.TypeString)),
			raw:  "",
			want: []string{},
		},
		{
			name:    "list bad element",
			typ:     listOf[int](typeOf[int](figureout.TypeInteger)),
			raw:     "1,x",
			wantErr: true,
		},
		{
			name:    "map unsupported",
			typ:     figureout.Type{Kind: figureout.TypeMap, Go: reflect.TypeFor[map[string]string]()},
			raw:     "a=b",
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse(tt.typ, tt.raw, "")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseSeparator(t *testing.T) {
	got, err := parse(listOf[string](typeOf[string](figureout.TypeString)), "a;b", ";")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, got)
}

func FuzzParse(f *testing.F) {
	types := []figureout.Type{
		typeOf[bool](figureout.TypeBoolean),
		typeOf[int](figureout.TypeInteger),
		typeOf[Port](figureout.TypeInteger),
		typeOf[float64](figureout.TypeNumber),
		typeOf[string](figureout.TypeString),
		typeOf[time.Duration](figureout.TypeDuration),
		typeOf[time.Time](figureout.TypeTimestamp),
		typeOf[[]byte](figureout.TypeBytes),
		listOf[int](typeOf[int](figureout.TypeInteger)),
	}
	for _, seed := range []string{
		"", "true", "42", "-1", "1.5", "hello", "1m30s", "2026-08-02T10:00:00Z", "aGk=", "1,2,3", "a;b",
	} {
		for i := range types {
			f.Add(i, seed)
		}
	}

	f.Fuzz(func(t *testing.T, idx int, raw string) {
		typ := types[((idx%len(types))+len(types))%len(types)]
		v, err := parse(typ, raw, "")
		if err != nil {
			return
		}
		// A successful parse must produce the field's Go type exactly, so that
		// materialization never has to guess.
		require.Equal(t, typ.Go, reflect.TypeOf(v))
	})
}
