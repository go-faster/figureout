package figureout

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
)

// Element paths name one element of a list or map inside a canonical path.
//
// A list whose elements are objects binds each element to its own path, so
// merging, provenance, defaults, constraints and erasure all reuse the per-path
// machinery rather than growing a parallel one for collections:
//
//	sites[0].max_bytes            an unkeyed list, by position
//	sites[name=docs].max_bytes    a list merged by key
//	proxies[gitlab].url           a map, by key
//
// The model spells the element itself with an empty subscript — "sites[]" —
// because a [FieldModel] describes every element rather than any one of them.
const (
	elementOpen  = '['
	elementClose = ']'
)

// ElementPath returns the path of one element of the collection at path.
func ElementPath(path, key string) string {
	return path + string(elementOpen) + key + string(elementClose)
}

// KeyedElementPath returns the path of a list element identified by a key
// field, as "sites[name=docs]".
func KeyedElementPath(path, field, value string) string {
	return ElementPath(path, field+"="+value)
}

// ElementKey returns the subscript of an element path, and the collection it
// belongs to.
func ElementKey(path string) (collection, key string, ok bool) {
	if path == "" || path[len(path)-1] != elementClose {
		return "", "", false
	}
	i := strings.LastIndexByte(path, elementOpen)
	if i < 0 {
		return "", "", false
	}
	return path[:i], path[i+1 : len(path)-1], true
}

// CanonicalPath rewrites every element subscript to the empty one, turning a
// concrete path into the model path describing it.
//
// "sites[name=docs].max_bytes" becomes "sites[].max_bytes", which is what
// [Model.FieldByPath] indexes.
func CanonicalPath(path string) string {
	if !strings.ContainsRune(path, elementOpen) {
		return path
	}
	var sb strings.Builder
	depth := 0
	for _, r := range path {
		switch {
		case r == elementOpen:
			depth++
			if depth == 1 {
				sb.WriteRune(r)
			}
		case r == elementClose:
			depth--
			if depth == 0 {
				sb.WriteRune(r)
			}
		case depth == 0:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// Collection is the value a source assigns to a list or map of objects itself,
// to say that this layer provided the collection.
//
// Its elements arrive as assignments of their own, so the merge policy needs
// some way to tell "this layer replaced the list" from "this layer said nothing
// about it" — an empty list has no elements to speak for it either way.
type Collection struct{}

// collection is the accumulated state of one list or map across layers.
type collection struct {
	// order is the element subscripts in the order they will materialize:
	// whatever the base layer set, then keys a later layer added.
	order []string
	// remap translates one layer's element subscripts to the accumulated ones.
	// A document's positions are its own; only a merge key survives a layer
	// boundary, which is the whole reason MergeByKey exists.
	remap map[string]string
	// provided records that some layer supplied the collection, which an empty
	// one has no elements to say for itself.
	provided bool
	origin   Origin
	erased   *Origin
}

func (c *collection) index(sub string) (int, bool) {
	for i, s := range c.order {
		if s == sub {
			return i, true
		}
	}
	return 0, false
}

func (c *collection) remove(sub string) {
	if i, ok := c.index(sub); ok {
		c.order = append(c.order[:i], c.order[i+1:]...)
	}
}

// resolution is the state accumulated while folding layers.
type resolution struct {
	values      map[string]*merged
	collections map[string]*collection
}

func newResolution() *resolution {
	return &resolution{
		values:      map[string]*merged{},
		collections: map[string]*collection{},
	}
}

func (r *resolution) collection(path string) *collection {
	c, ok := r.collections[path]
	if !ok {
		c = &collection{}
		r.collections[path] = c
	}
	return c
}

// clear drops a collection and everything under it.
func (r *resolution) clear(path string) {
	if c, ok := r.collections[path]; ok {
		c.order = nil
	}
	prefix := path + string(elementOpen)
	for p := range r.values {
		if strings.HasPrefix(p, prefix) {
			delete(r.values, p)
		}
	}
}

// clearElement drops one element and everything under it.
func (r *resolution) clearElement(listPath, sub string) {
	if c, ok := r.collections[listPath]; ok {
		c.remove(sub)
	}
	elem := ElementPath(listPath, sub)
	for p := range r.values {
		if p == elem || strings.HasPrefix(p, elem+".") {
			delete(r.values, p)
		}
	}
}

// resolveElements rewrites a layer's element subscripts into the accumulated
// ones, allocating an element the first time this layer names it.
//
// A map entry and a keyed list element identify themselves, so their subscript
// carries across layers unchanged. An unkeyed list element only has its
// position, which belongs to the document it came from: it is allocated a fresh
// slot instead, which is what makes replace and append well defined and keyed
// merge the only way to edit an element in place.
func (m *Model) resolveElements(res *resolution, path string) (string, bool) {
	out := ""
	rest := path
	for {
		open := strings.IndexByte(rest, elementOpen)
		if open < 0 {
			return out + rest, true
		}
		closing := strings.IndexByte(rest[open:], elementClose)
		if closing < 0 {
			return out + rest, true
		}
		closing += open

		listPath := out + rest[:open]
		sub := rest[open+1 : closing]
		rest = rest[closing+1:]

		f, ok := m.FieldByPath(listPath)
		if !ok {
			return "", false
		}
		c := res.collection(listPath)
		if c.remap == nil {
			c.remap = map[string]string{}
		}

		final, ok := c.remap[sub]
		if !ok {
			final = sub
			if _, keyed := f.MergeKey(); !keyed && f.Type.Kind == TypeList {
				// Positions do not survive a layer boundary.
				final = strconv.Itoa(len(c.order))
			}
			c.remap[sub] = final
			if _, exists := c.index(final); !exists {
				c.order = append(c.order, final)
			}
		}
		out = ElementPath(listPath, final)
	}
}

// moveCollections re-roots collection state from one path prefix to another, so
// a former path that carried a list hands over its elements and their order.
func (r *resolution) moveCollections(from, to string) {
	prefix := from + "."
	for path, c := range r.collections {
		if path != from && !strings.HasPrefix(path, prefix) {
			continue
		}
		r.collections[to+path[len(from):]] = c
		delete(r.collections, path)
	}
}

// startLayer forgets the previous layer's subscript translation.
func (r *resolution) startLayer() {
	for _, c := range r.collections {
		c.remap = nil
	}
}

// foldCollection folds an assignment that is about a collection or one of its
// elements.
//
// It reports whether the assignment was fully handled, and otherwise the path
// the assignment should fold under, which differs from the one the source wrote
// whenever an element had to be allocated a slot.
func (m *Model) foldCollection(res *resolution, a Assignment, f *FieldModel, rep *Report) (handled bool, path string) {
	resolved := a.Path
	if strings.ContainsRune(a.Path, elementOpen) {
		// A collection nested in an element carries a subscript of its own, so
		// its slot has to be allocated before the marker can be recognized.
		var ok bool
		if resolved, ok = m.resolveElements(res, a.Path); !ok {
			return true, ""
		}
	}

	// The collection itself: a marker saying this layer provided it, or a null
	// erasing it outright.
	if f != nil && CanonicalPath(resolved) == f.Path {
		if _, ok := collectionOf(f); ok {
			a.Path = resolved
			m.foldCollectionItself(res, a, f)
			return true, ""
		}
	}

	if resolved == a.Path && !strings.ContainsRune(a.Path, elementOpen) {
		return false, ""
	}

	// A null at an element path removes the element, which is how a later layer
	// drops one entry of a map without restating the rest.
	if a.State == ValueNull {
		if listPath, sub, isElement := ElementKey(resolved); isElement {
			res.clearElement(listPath, sub)
			rep.erased[resolved] = a.Origin
			return true, ""
		}
	}
	return false, resolved
}

func (m *Model) foldCollectionItself(res *resolution, a Assignment, f *FieldModel) {
	switch {
	case a.State == ValueNull:
		res.clear(a.Path)
		origin := a.Origin
		c := res.collection(a.Path)
		c.erased = &origin
		// Erased is not provided: the field falls back the way an absent one
		// does, which for a collection is an empty one.
		c.provided = false
		return
	case f.Merge == MergeReplace:
		// A layer that provides the list provides all of it: the elements it
		// does not mention are gone, which is what replace means.
		res.clear(a.Path)
	}
	// Append keeps what earlier layers contributed; MergeByKey identifies
	// elements by their key, so neither drops anything here.
	c := res.collection(a.Path)
	c.provided = true
	c.origin = a.Origin
	c.erased = nil
}

// parseKey converts a map entry's subscript back into its Go key type.
//
// Keys arrive as the text a document spelled them with. The core parses them
// itself rather than through a source's scalar rules: a key is part of the
// path, and a path is format-neutral.
func parseKey(t Type, text string) (any, error) {
	rv := reflect.New(t.Go).Elem()
	switch t.Go.Kind() {
	case reflect.String:
		rv.SetString(text)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(text, 10, t.Go.Bits())
		if err != nil {
			return nil, errors.Errorf("invalid integer key %q", text)
		}
		rv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(text, 10, t.Go.Bits())
		if err != nil {
			return nil, errors.Errorf("invalid unsigned integer key %q", text)
		}
		rv.SetUint(n)
	default:
		return nil, errors.Errorf("cannot use %s as a map key", t.Go)
	}
	return rv.Interface(), nil
}

// collectionOf reports whether a field is a list or map whose elements are
// described objects, and returns the element object.
func collectionOf(f *FieldModel) (*ObjectModel, bool) {
	if f.Type.Kind != TypeList && f.Type.Kind != TypeMap {
		return nil, false
	}
	if f.Type.Elem == nil || f.Type.Elem.Object == nil {
		return nil, false
	}
	return f.Type.Elem.Object, true
}

// Elements reports whether the field is a collection of described objects.
//
// Sources use it to decide whether to bind elements to their own paths; one
// that cannot express a collection of objects, such as environment variables,
// skips the field instead of inventing an index convention.
func (f *FieldModel) Elements() (*ObjectModel, bool) { return collectionOf(f) }

// MergeKey returns the element field identifying a list element across layers,
// as set by [ListField.MergeByKey].
func (f *FieldModel) MergeKey() (*FieldModel, bool) {
	if f.mergeKey == nil {
		return nil, false
	}
	return f.mergeKey, true
}
