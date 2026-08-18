package figureout

import (
	"reflect"
	"strings"
)

// Section is the value a source assigns to an optional object itself, to say
// that this layer contained the section.
//
// A section that is there and defaulted throughout has no member assignment to
// speak for it, and a nil pointer has to keep meaning "no section": without a
// marker of its own, "s3: {}" and no "s3" key at all would resolve alike. It is
// [Collection] and [Element] one level up, for the same reason.
//
// A source with no nesting has nothing to write it with, so an optional section
// is present there whenever anything under it is. See [Model.sectionPresent].
type Section struct{}

// sectionPresent reports whether any layer contained the optional object at
// path.
//
// A nesting source says so with a [Section] marker at the object's own path. A
// flat source such as environment variables cannot: it has names for the
// members and none for the section, so a member it did provide is the whole of
// what it can say. Both answers are the same statement, and taking either keeps
// a value from being dropped for want of a marker no source could write.
func (m *Model) sectionPresent(path string, values map[string]Assignment, res *resolution) bool {
	if a, ok := values[path]; ok && a.State == ValuePresent {
		return true
	}
	prefix := path + "."
	for p, a := range values {
		if a.State == ValuePresent && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	for p, c := range res.collections {
		if c.provided && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// materializeOptionalObject allocates a section only when some layer contained
// one, so nil keeps meaning "no section" rather than "a section of zeroes".
func (m *Model) materializeOptionalObject(
	f *FieldModel,
	v reflect.Value,
	path string,
	values map[string]Assignment,
	res *resolution,
	rep *Report,
) {
	if !m.sectionPresent(path, values, res) {
		// The pointer stays nil, and the members are never materialized, so a
		// member registered with Explicit is demanded inside a section that is
		// there rather than everywhere.
		return
	}

	ev := reflect.New(f.Type.Go).Elem()
	m.materialize(f.Type.Object, ev, path+".", values, res, rep)
	if err := f.acc.set(v, ev.Interface()); err != nil {
		rep.diag(f, path, nil, CodeConstraintMismatch, err.Error())
		return
	}
	if a, ok := values[path]; ok {
		rep.origins[path] = a.Origin
	}
}

// dropSubtree forgets everything folded under a path, which is what erasing an
// optional section has to do: the section is gone, and so is what earlier
// layers wrote inside it.
func (r *resolution) dropSubtree(path string) {
	prefix := path + "."
	for p := range r.values {
		if strings.HasPrefix(p, prefix) {
			delete(r.values, p)
		}
	}
	for p := range r.collections {
		if strings.HasPrefix(p, prefix) {
			delete(r.collections, p)
		}
	}
}
