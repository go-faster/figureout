package figureout

import (
	"reflect"

	"github.com/go-faster/errors"
)

// MergePolicy decides how a field combines values from several layers.
//
// The default is [MergeReplace], because a predictable "last one wins" is what
// a reader of a layered configuration can reason about. The other policies
// exist for the collections where accumulating across layers is the point.
type MergePolicy uint8

// Merge policies.
const (
	// MergeReplace takes the value from the last layer that provided one.
	MergeReplace MergePolicy = iota
	// MergeAppend concatenates list values across layers, in layer order.
	MergeAppend
	// MergeByKey merges map entries across layers, so a later layer changes
	// only the keys it names.
	MergeByKey
)

// String implements [fmt.Stringer].
func (p MergePolicy) String() string {
	switch p {
	case MergeAppend:
		return "append"
	case MergeByKey:
		return "by-key"
	default:
		return "replace"
	}
}

// Applies reports whether the policy is meaningful for kind.
func (p MergePolicy) Applies(kind TypeKind) bool {
	switch p {
	case MergeAppend:
		return kind == TypeList
	case MergeByKey:
		return kind == TypeMap
	default:
		return true
	}
}

// merged is the accumulated state of one configuration path across layers.
type merged struct {
	assignment Assignment
	// erased records the layer that last erased the path, so a diagnostic can
	// say where a value went.
	erased *Origin
	set    bool
}

// mergeSet folds one assignment into the accumulated state.
//
// A null assignment is an erase directive rather than a value: it drops what
// earlier layers provided, so the field falls back to its default or to
// missing. That is the meaning a layered configuration can actually use, and
// it keeps null out of the resolved Go value entirely.
func mergeSet(state *merged, a Assignment, policy MergePolicy) error {
	switch a.State {
	case ValueMissing:
		return nil

	case ValueNull:
		origin := a.Origin
		*state = merged{erased: &origin}
		return nil

	default:
		if !state.set || policy == MergeReplace {
			*state = merged{assignment: a, set: true}
			return nil
		}

		combined, err := combine(policy, state.assignment.Value, a.Value)
		if err != nil {
			return err
		}
		next := a
		next.Value = combined
		*state = merged{assignment: next, set: true}
		return nil
	}
}

func combine(policy MergePolicy, previous, next any) (any, error) {
	switch policy {
	case MergeAppend:
		return appendValues(previous, next)
	case MergeByKey:
		return mergeByKey(previous, next)
	default:
		return next, nil
	}
}

func appendValues(previous, next any) (any, error) {
	pv, nv := reflect.ValueOf(previous), reflect.ValueOf(next)
	if pv.Kind() != reflect.Slice || nv.Kind() != reflect.Slice {
		return nil, errors.Errorf("cannot append %T to %T", next, previous)
	}
	if pv.Type() != nv.Type() {
		return nil, errors.Errorf("cannot append %s to %s", nv.Type(), pv.Type())
	}

	out := reflect.MakeSlice(pv.Type(), 0, pv.Len()+nv.Len())
	out = reflect.AppendSlice(out, pv)
	out = reflect.AppendSlice(out, nv)
	return out.Interface(), nil
}

func mergeByKey(previous, next any) (any, error) {
	pv, nv := reflect.ValueOf(previous), reflect.ValueOf(next)
	if pv.Kind() != reflect.Map || nv.Kind() != reflect.Map {
		return nil, errors.Errorf("cannot merge %T into %T", next, previous)
	}
	if pv.Type() != nv.Type() {
		return nil, errors.Errorf("cannot merge %s into %s", nv.Type(), pv.Type())
	}

	out := reflect.MakeMapWithSize(pv.Type(), pv.Len())
	for iter := pv.MapRange(); iter.Next(); {
		out.SetMapIndex(iter.Key(), iter.Value())
	}
	for iter := nv.MapRange(); iter.Next(); {
		out.SetMapIndex(iter.Key(), iter.Value())
	}
	return out.Interface(), nil
}
