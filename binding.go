package figureout

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"github.com/go-faster/errors"
)

// FieldPath locates a Go field relative to the object declaring it.
//
// Index is the canonical identity: it survives padding changes and is usable
// with [reflect.Value.FieldByIndex]. Offset is kept for pointer validation and
// fast access, and is never the sole identity.
type FieldPath struct {
	Index  []int
	Offset uintptr
}

// String implements [fmt.Stringer].
func (p FieldPath) String() string {
	parts := make([]string, len(p.Index))
	for i, idx := range p.Index {
		parts[i] = strconv.Itoa(idx)
	}
	return strings.Join(parts, ".")
}

// bound is one candidate Go field discovered inside the root type.
type bound struct {
	index   []int
	typ     reflect.Type
	goPath  string
	name    string
	offset  uintptr
	skipped bool // unexported and therefore not settable by reflection
}

// binder resolves field pointers taken inside a synthetic root value back to
// reflection index paths.
type binder struct {
	root reflect.Type
	// goPath prefixes every discovered field, so a nested builder reports
	// "Config.Server.Port" rather than "Server.Port".
	goPath   string
	base     uintptr
	size     uintptr
	byOffset map[uintptr][]bound
	all      []bound
}

func newBinder(root reflect.Value, goPath string) *binder {
	t := root.Type()
	b := &binder{
		root:     t,
		goPath:   goPath,
		base:     root.Addr().Pointer(),
		size:     t.Size(),
		byOffset: map[uintptr][]bound{},
	}
	b.walk(t, nil, goPath, 0)
	for _, c := range b.all {
		b.byOffset[c.offset] = append(b.byOffset[c.offset], c)
	}
	return b
}

// walk enumerates every field reachable by direct embedding, recording its
// offset relative to the root object.
func (b *binder) walk(t reflect.Type, index []int, goPath string, offset uintptr) {
	for i := range t.NumField() {
		f := t.Field(i)
		idx := append(slices.Clone(index), i)
		path := goPath + "." + f.Name
		off := offset + f.Offset

		b.all = append(b.all, bound{
			index:   idx,
			typ:     f.Type,
			goPath:  path,
			name:    f.Name,
			offset:  off,
			skipped: !f.IsExported(),
		})

		if f.Type.Kind() == reflect.Struct && !isOpaqueStruct(f.Type) {
			b.walk(f.Type, idx, path, off)
		}
	}
}

// isOpaqueStruct reports whether a struct should be treated as a leaf value
// rather than as a nested object.
func isOpaqueStruct(t reflect.Type) bool {
	if t == timeType {
		return true
	}
	_, ok := reflect.New(t).Elem().Interface().(carrierInfo)
	return ok
}

// resolve maps a pointer taken inside the synthetic root to a field path.
func (b *binder) resolve(ptr unsafe.Pointer, typ reflect.Type, name string) (bound, error) {
	addr := uintptr(ptr)
	if b.size == 0 || addr < b.base || addr >= b.base+b.size {
		return bound{}, &Diagnostic{
			Severity:  SeverityError,
			Code:      CodeForeignPointer,
			FieldPath: name,
			Message: errors.Errorf(
				"pointer does not refer to a field inside %s; it may point to a copy or to an unrelated variable",
				b.root,
			).Error(),
		}
	}

	offset := addr - b.base
	var matches []bound
	for _, c := range b.byOffset[offset] {
		if c.typ == typ {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return bound{}, &Diagnostic{
			Severity:  SeverityError,
			Code:      CodeForeignPointer,
			FieldPath: name,
			Message: errors.Errorf(
				"no field of type %s at offset %d in %s; the pointer may refer to a copy, or the registration helper may not match the field type",
				typ, offset, b.root,
			).Error(),
		}
	default:
		paths := make([]string, len(matches))
		for i, m := range matches {
			paths[i] = m.goPath
		}
		return bound{}, &Diagnostic{
			Severity:  SeverityError,
			Code:      CodeAmbiguousZeroSize,
			FieldPath: name,
			Message: errors.Errorf(
				"pointer is ambiguous between %s; register zero-sized fields by path instead",
				strings.Join(paths, ", "),
			).Error(),
		}
	}
}

// lookupPath resolves a dotted Go field path such as "Server.Port".
func (b *binder) lookupPath(path string) (bound, error) {
	want := b.goPath + "." + path
	for _, c := range b.all {
		if c.goPath == want {
			return c, nil
		}
	}
	return bound{}, errors.Errorf("no field %q in %s", path, b.root)
}

// children returns the fields declared directly by the given index path.
func (b *binder) children(index []int) []bound {
	var out []bound
	for _, c := range b.all {
		if len(c.index) == len(index)+1 && slices.Equal(c.index[:len(index)], index) {
			out = append(out, c)
		}
	}
	return out
}
