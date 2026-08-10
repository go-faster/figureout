package figureout

// SourceNamer reports the names a source accepts for every field.
//
// A name belongs to the source rather than to the model: the environment
// source joins the segments of a path, applies its own naming and prepends the
// prefix the caller configured, so only that source knows "server.port" is read
// from APP_SERVER_LISTEN_PORT. A target that documents names asks the source
// for them instead of re-deriving them, which is what keeps documentation from
// drifting from what is actually read.
//
// A configured source is therefore the unit that can answer, not a [SourceID].
type SourceNamer interface {
	Source

	// ProjectNames maps a canonical field path to the names this source
	// accepts for it, primary first. A field the source cannot read, such as a
	// list of objects in the environment, is absent from the result.
	ProjectNames(m *Model) map[string][]string
}
