package figureout

// SourceID identifies an input mechanism such as JSON or environment variables.
type SourceID string

// TargetID identifies an output representation such as JSON Schema or CUE.
type TargetID string

// ShapeKind is a wire-level representation kind.
//
// It describes what a source accepts, not what the value means; see [TypeKind]
// for semantics.
type ShapeKind uint8

// Shape kinds.
const (
	ShapeUnknown ShapeKind = iota
	ShapeNull
	ShapeBoolean
	ShapeInteger
	ShapeNumber
	ShapeString
	ShapeArray
	ShapeObject
)

// String implements [fmt.Stringer].
func (k ShapeKind) String() string {
	switch k {
	case ShapeNull:
		return "null"
	case ShapeBoolean:
		return "boolean"
	case ShapeInteger:
		return "integer"
	case ShapeNumber:
		return "number"
	case ShapeString:
		return "string"
	case ShapeArray:
		return "array"
	case ShapeObject:
		return "object"
	default:
		return "unknown"
	}
}

// Shape is a wire representation accepted or produced by a source.
type Shape struct {
	Kind ShapeKind

	Elem   *Shape
	Key    *Shape
	Fields map[string]Shape
	OneOf  []Shape
}

// Decoder converts a raw source value into a semantic value.
//
// A decoder is opaque, so it must be accompanied by the shapes it accepts;
// otherwise schema generation cannot describe the field.
type Decoder interface {
	DecodeValue(raw any) (any, error)
}

// Encoder converts a semantic value back into a raw source value.
type Encoder interface {
	EncodeValue(v any) (any, error)
}

// SourceProjection is how one field is represented and decoded by one source.
type SourceProjection struct {
	Source SourceID
	// Names lists the accepted names, primary first, aliases after.
	Names []string
	// Accepts lists the wire shapes the source accepts for this field. When
	// empty, the shape is derived from the semantic type.
	Accepts []Shape
	// Skip excludes the field from this source entirely.
	Skip bool

	Decoder Decoder
	Encoder Encoder
	// Options carries source-specific settings, owned by the source package.
	Options []any
}

// Name returns the primary name of the projection.
func (p *SourceProjection) Name() string {
	if len(p.Names) == 0 {
		return ""
	}
	return p.Names[0]
}

// DeriveShapes returns the accepted shapes, falling back to the shape implied
// by the semantic type when the source declares none.
func (p *SourceProjection) DeriveShapes(t Type) []Shape {
	if len(p.Accepts) > 0 {
		return p.Accepts
	}
	return []Shape{semanticShape(t)}
}

func semanticShape(t Type) Shape {
	switch t.Kind {
	case TypeBoolean:
		return Shape{Kind: ShapeBoolean}
	case TypeInteger:
		return Shape{Kind: ShapeInteger}
	case TypeNumber:
		return Shape{Kind: ShapeNumber}
	case TypeDuration:
		// A unit-scaled duration is written as a count of its unit.
		if t.Unit > 0 {
			return Shape{Kind: ShapeInteger}
		}
		return Shape{Kind: ShapeString}
	case TypeString, TypeBytes, TypeTimestamp:
		return Shape{Kind: ShapeString}
	case TypeList:
		elem := semanticShape(*t.Elem)
		return Shape{Kind: ShapeArray, Elem: &elem}
	case TypeMap:
		elem := semanticShape(*t.Elem)
		return Shape{Kind: ShapeObject, Elem: &elem}
	case TypeObject, TypeUnion:
		return Shape{Kind: ShapeObject}
	default:
		return Shape{Kind: ShapeUnknown}
	}
}
