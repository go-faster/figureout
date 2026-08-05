package figureout

// Group opens a configuration path level that has no Go struct behind it.
//
// The shape that reads well in a file and the shape a consumer wants in Go are
// not always the same shape. A group registers flat Go fields under a nested
// path, so neither side has to be reshaped to match the other:
//
//	figureout.Group(s, "webhook", func(s *figureout.Schema[GitLab]) {
//		figureout.Value(s, &c.WebhookEnabled, "enabled").ApplyDefault(false)
//		figureout.Value(s, &c.WebhookSecret, "secret", figureout.Hidden())
//	})
//
//	webhook:
//	  enabled: true
//	  secret: hunter2
//
// Only the path nests: the fields still bind to the same struct, so the
// completeness and duplicate-registration checks see exactly the fields they
// would have seen without the group. A group contributes a segment everywhere a
// nested object would, including environment variable names
// (GITLAB_WEBHOOK_SECRET) and generated schemas.
//
// Registering a nested Go struct's fields as siblings of the parent — the
// inverse mismatch — needs no dedicated function: register the descendants
// directly, as in figureout.Value(s, &c.Database.DSN, "dsn").
func Group[T any](s *Schema[T], name string, describe func(*Schema[T]), opts ...FieldOption) *ObjectField {
	b := s.b
	reg := &registration{kind: regGroup, name: name, goName: b.goPath, group: &container{}}
	field := &ObjectField{&FieldBuilder{b: b, reg: reg}}

	switch {
	case name == "":
		b.diags.errorf(CodeMissingDefinition, b.goPath, "", "empty group name")
		return field
	case describe == nil:
		b.diags.errorf(CodeMissingDefinition, b.goPath, name, "nil describe function for group %q", name)
		return field
	}

	reg.valid = true
	b.add(reg)

	b.stack = append(b.stack, reg.group)
	describe(s)
	b.stack = b.stack[:len(b.stack)-1]

	b.applyOptions(reg, opts)
	return field
}
