package jsonschema

// allowSchemaKey declares the schema reference on the root object.
//
// The root is closed with additionalProperties, so without this the schema
// rejects the very member that attaches it to a document. A descriptor that
// registers "$schema" itself keeps its own declaration.
func allowSchemaKey(doc map[string]any) {
	props, ok := doc[keyProperties].(map[string]any)
	if !ok {
		return
	}
	if _, ok := props[keySchema]; ok {
		return
	}
	props[keySchema] = map[string]any{
		keyType: typeString,
	}
}
