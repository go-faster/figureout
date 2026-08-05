package figureout

import (
	"slices"
	"strings"
)

// placeMoved inserts a deprecated shadow field for every former path declared
// with [MovedFrom].
//
// The walk stays inside the objects this builder owns — its own containers and
// the groups they open. A nested descriptor placed its own shadows when it was
// derived, and one shared by several parents must not be modified from here.
func (b *builder) placeMoved(root *ObjectModel) {
	var targets []*FieldModel
	var collect func(o *ObjectModel)
	collect = func(o *ObjectModel) {
		for _, f := range o.Fields {
			if len(f.MovedFrom) > 0 {
				targets = append(targets, f)
			}
			if isGroup(f) {
				collect(f.Type.Object)
			}
		}
	}
	collect(root)

	for _, f := range targets {
		for _, old := range f.MovedFrom {
			b.placeShadow(root, f, old)
		}
	}
}

// isGroup reports whether a field opens a path level without a Go one.
func isGroup(f *FieldModel) bool {
	return f.Type.Object != nil && len(f.GoPath.Index) == 0
}

func (b *builder) placeShadow(root *ObjectModel, target *FieldModel, old string) {
	segments := strings.Split(old, ".")
	obj := root

	for i, seg := range segments[:len(segments)-1] {
		existing := member(obj, seg)
		switch {
		case existing == nil:
			// The level itself is gone: rebuild it as a deprecated group, so
			// the old nesting still parses and still shows up as deprecated.
			level := &FieldModel{
				Name:     seg,
				Path:     seg,
				GoName:   b.goPath,
				Presence: PresenceOptional,
				Meta:     Metadata{Deprecated: "moved to " + target.GoName},
				Type:     Type{Kind: TypeObject, Go: b.rootType, Object: &ObjectModel{Go: b.rootType}},
			}
			obj.Fields = append(obj.Fields, level)
			obj = level.Type.Object
		case isGroup(existing):
			obj = existing.Type.Object
		default:
			b.diags.errorf(CodeDuplicateName, target.GoName, old,
				"former path %q of %s passes through %q, which is not a group",
				old, target.Name, strings.Join(segments[:i+1], "."))
			return
		}
	}

	name := segments[len(segments)-1]
	if prev := member(obj, name); prev != nil {
		// Resolving to the field itself is the mistake a reader makes first:
		// the old spelling was at the document root, but the field is declared
		// by a nested descriptor, where a former path cannot reach the root.
		if prev == target {
			b.diags.errorf(CodeDuplicateName, target.GoName, old,
				"former path %q is relative to the descriptor declaring the field (%s), "+
					"where it resolves to the field itself; "+
					"declare the field with Group if the old spelling was at the document root",
				old, b.goPath)
			return
		}
		b.diags.errorf(CodeDuplicateName, target.GoName, old,
			"former path %q of %s is already a configuration property of %s; "+
				"former paths are relative to the descriptor declaring the field",
			old, target.Name, b.goPath)
		return
	}

	obj.Fields = append(obj.Fields, &FieldModel{
		Name:     name,
		Path:     name,
		GoName:   target.GoName,
		Type:     target.Type,
		Presence: PresenceOptional,
		Merge:    target.Merge,
		Meta:     Metadata{Hidden: true},
		Sources:  skipsOf(target),
		movedTo:  target,
	})
}

// member finds a field by name before the model is indexed.
func member(o *ObjectModel, name string) *FieldModel {
	for _, f := range o.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// skipsOf carries a field's per-source exclusions onto its shadow, so a field
// hidden from a source does not reappear there under its old name. Names are
// deliberately not carried: they belong to the current spelling.
func skipsOf(f *FieldModel) map[SourceID]*SourceProjection {
	var out map[SourceID]*SourceProjection
	for id, p := range f.Sources {
		if !p.Skip {
			continue
		}
		if out == nil {
			out = map[SourceID]*SourceProjection{}
		}
		out[id] = &SourceProjection{Source: id, Skip: true}
	}
	return out
}

// applyMoved redirects values written under a former path onto the field that
// superseded it.
//
// Both spellings at once is an error rather than a precedence rule: they are
// two intentions in one configuration, and picking one silently is the answer
// most likely to be wrong.
func (m *Model) applyMoved(res *resolution, rep *Report) {
	state := res.values
	for _, f := range m.fields {
		target := f.movedTo
		if target == nil {
			continue
		}

		// A former path covers a whole subtree, not only its own value: a
		// nested object, or the object spelling of a [ScalarOr] field, sets its
		// members rather than the field itself.
		moved := setUnder(state, f.Path)
		if len(moved) == 0 {
			continue
		}
		origin := state[moved[0]].assignment.Origin

		if conflicting := setUnder(state, target.Path); len(conflicting) > 0 {
			rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
				Severity:  SeverityError,
				Code:      CodeMovedConflict,
				FieldPath: f.Path,
				GoPath:    target.GoName,
				Message: "moved to " + target.Path + ", and both are set (" +
					state[conflicting[0]].assignment.Origin.String() +
					"); remove the deprecated spelling",
				Origin: &origin,
			})
			continue
		}

		rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
			Severity:  SeverityWarning,
			Code:      CodeDeprecated,
			FieldPath: f.Path,
			GoPath:    target.GoName,
			Message:   "deprecated, use " + target.Path,
			Origin:    &origin,
		})

		if ts, ok := state[target.Path]; ok && ts.erased != nil {
			// A later layer erased the field outright; the old spelling does
			// not resurrect it.
			continue
		}
		for _, path := range moved {
			rest := path[len(f.Path):]
			assignment := state[path].assignment
			assignment.Path = target.Path + rest
			state[target.Path+rest] = &merged{assignment: assignment, set: true}
			delete(state, path)
		}
		res.moveCollections(f.Path, target.Path)
	}
}

// setUnder returns the paths carrying a value at path or below it, in a stable
// order.
func setUnder(state map[string]*merged, path string) []string {
	var out []string
	if st, ok := state[path]; ok && st.set {
		out = append(out, path)
	}
	prefix := path + "."
	for p, st := range state {
		if st.set && strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
