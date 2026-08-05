package figureout

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"strings"

	"github.com/go-faster/errors"
)

// Origin records where a value came from.
type Origin struct {
	Source SourceID
	// Name is the source-specific name, such as an environment variable.
	Name string
	File string
	Line int
	Col  int
}

// String implements [fmt.Stringer].
func (o Origin) String() string {
	var sb strings.Builder
	if o.Source != "" {
		sb.WriteString(string(o.Source))
	}
	if o.Name != "" {
		fmt.Fprintf(&sb, " %s", o.Name)
	}
	if o.File != "" {
		fmt.Fprintf(&sb, " %s", o.File)
		if o.Line > 0 {
			fmt.Fprintf(&sb, ":%d", o.Line)
			if o.Col > 0 {
				fmt.Fprintf(&sb, ":%d", o.Col)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// ValueState distinguishes a missing value from an explicit null.
type ValueState uint8

// Value states.
const (
	ValueMissing ValueState = iota
	ValueNull
	ValuePresent
)

// Assignment is one value produced by a source.
type Assignment struct {
	// Path is the canonical dotted path of the field.
	Path   string
	State  ValueState
	Value  any
	Origin Origin
}

// Layer is the partial configuration decoded from one source.
//
// Sources never write into the destination struct: they produce layers, which
// are merged in precedence order before materialization.
type Layer struct {
	Source      SourceID
	Assignments []Assignment
	Diagnostics Diagnostics
}

// Set appends an assignment.
func (l *Layer) Set(path string, v any, origin Origin) {
	l.Assignments = append(l.Assignments, Assignment{
		Path: path, State: ValuePresent, Value: v, Origin: origin,
	})
}

// SetNull appends an explicit null assignment.
func (l *Layer) SetNull(path string, origin Origin) {
	l.Assignments = append(l.Assignments, Assignment{
		Path: path, State: ValueNull, Origin: origin,
	})
}

// Source decodes a configuration layer.
type Source interface {
	ID() SourceID
	Load(ctx context.Context, m *Model) (*Layer, error)
}

// Report describes how a configuration was resolved.
type Report struct {
	Diagnostics Diagnostics

	origins map[string]Origin
	erased  map[string]Origin
	secrets map[string]struct{}
}

// OriginOf returns where the value at the canonical path came from.
func (r *Report) OriginOf(path string) (Origin, bool) {
	o, ok := r.origins[path]
	return o, ok
}

// ErasedBy returns the layer that erased the value at the canonical path.
//
// A source spells an erase as an explicit null; the field then falls back to
// its default, or stays missing.
func (r *Report) ErasedBy(path string) (Origin, bool) {
	o, ok := r.erased[path]
	return o, ok
}

// Origins iterates over every resolved path and its origin.
func (r *Report) Origins() iter.Seq2[string, Origin] {
	return func(yield func(string, Origin) bool) {
		for path, o := range r.origins {
			if !yield(path, o) {
				return
			}
		}
	}
}

// DiscriminatorPath returns the canonical path of a union field's
// discriminator property.
func DiscriminatorPath(f *FieldModel) (string, bool) {
	if f.Type.Union == nil {
		return "", false
	}
	return f.Path + "." + f.Type.Union.Discriminator, true
}

// Resolve decodes, merges, validates and materializes a configuration.
//
// Later sources override earlier ones. The returned report carries provenance
// and diagnostics even when an error is returned.
func (d *Descriptor[T]) Resolve(sources ...Source) (T, *Report, error) {
	return d.ResolveContext(context.Background(), sources...)
}

// ResolveContext is [Descriptor.Resolve] with a context.
func (d *Descriptor[T]) ResolveContext(ctx context.Context, sources ...Source) (T, *Report, error) {
	var cfg T
	rep := &Report{
		origins: map[string]Origin{},
		erased:  map[string]Origin{},
		secrets: map[string]struct{}{},
	}

	state := map[string]*merged{}
	for _, src := range sources {
		if src == nil {
			continue
		}
		layer, err := src.Load(ctx, d.model)
		if err != nil {
			return cfg, rep, errors.Wrapf(err, "source %q", src.ID())
		}
		if layer == nil {
			continue
		}
		rep.Diagnostics = append(rep.Diagnostics, layer.Diagnostics...)
		d.model.fold(state, layer, rep)
	}
	d.model.applyMoved(state, rep)
	if err := rep.Diagnostics.Err(); err != nil {
		return cfg, rep, err
	}

	values := make(map[string]Assignment, len(state))
	for path, st := range state {
		if st.erased != nil {
			rep.erased[path] = *st.erased
		}
		if st.set {
			values[path] = st.assignment
		}
	}

	rv := reflect.ValueOf(&cfg).Elem()
	d.model.materialize(d.model.Root, rv, values, rep)
	if err := rep.Diagnostics.Err(); err != nil {
		var zero T
		return zero, rep, err
	}

	// Cross-field rules run last, on a configuration whose every field already
	// resolved and validated, so a violation is never a knock-on effect.
	d.checkInvariants(rv, rep)
	if err := rep.Diagnostics.Err(); err != nil {
		var zero T
		return zero, rep, err
	}
	return cfg, rep, nil
}

// fold merges one layer into the accumulated state, honoring each field's
// merge policy.
func (m *Model) fold(state map[string]*merged, layer *Layer, rep *Report) {
	for _, a := range layer.Assignments {
		if a.State == ValueMissing {
			continue
		}

		policy := MergeReplace
		var f *FieldModel
		if found, ok := m.FieldByPath(a.Path); ok {
			f, policy = found, found.Merge
		}

		// A [ScalarOr] field written both ways does not merge: the two
		// spellings describe the same value, so the later one replaces the
		// other outright rather than half-filling an object.
		m.dropOtherSpelling(state, a.Path)

		st, ok := state[a.Path]
		if !ok {
			st = &merged{}
			state[a.Path] = st
		}
		if err := mergeSet(st, a, policy); err != nil {
			origin := a.Origin
			path, goPath := a.Path, ""
			if f != nil {
				goPath = f.GoName
			}
			rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
				Severity:  SeverityError,
				Code:      CodeConstraintMismatch,
				Message:   err.Error(),
				FieldPath: path,
				GoPath:    goPath,
				Origin:    &origin,
			})
		}
	}
}

// Value reads the value at a canonical path out of a resolved configuration.
//
// It reports false when the path is unknown, when the value is carried by an
// unset [OptionalOf], or when the path is inside a variant that was not selected.
func (d *Descriptor[T]) Value(cfg *T, path string) (any, bool) {
	if cfg == nil {
		return nil, false
	}
	f, obj, ok := d.model.lookup(reflect.ValueOf(cfg).Elem(), path)
	if !ok {
		return nil, false
	}
	return f.acc.get(obj)
}

// lookup walks down to the field at path and the Go value declaring it.
//
// The walk stops at an unselected union variant, so a path that exists in the
// model but not in this value reports false rather than reading a sibling
// variant's memory.
func (m *Model) lookup(root reflect.Value, path string) (*FieldModel, reflect.Value, bool) {
	obj, v := m.Root, root
	for {
		name, rest, nested := strings.Cut(path, ".")
		f, ok := obj.Field(name)
		if !ok {
			return nil, reflect.Value{}, false
		}
		if !nested {
			if f.movedTo != nil {
				return nil, reflect.Value{}, false
			}
			return f, v, true
		}
		switch {
		case f.Type.Object != nil:
			obj, v = f.Type.Object, v.FieldByIndex(f.GoPath.Index)
		case f.Type.Union != nil:
			variant, child, ok := selectedVariant(f, v)
			if !ok {
				return nil, reflect.Value{}, false
			}
			obj, v = variant.Object, child
		default:
			return nil, reflect.Value{}, false
		}
		path = rest
	}
}

// selectedVariant returns the variant whose pointer is non-nil.
func selectedVariant(f *FieldModel, v reflect.Value) (*VariantModel, reflect.Value, bool) {
	for _, variant := range f.Type.Union.Variants {
		fv := v.FieldByIndex(variant.acc.index)
		if !fv.IsNil() {
			return variant, fv.Elem(), true
		}
	}
	return nil, reflect.Value{}, false
}

func (m *Model) materialize(o *ObjectModel, v reflect.Value, values map[string]Assignment, rep *Report) {
	for _, f := range o.Fields {
		if f.movedTo != nil {
			// A former spelling never reaches the Go value: applyMoved has
			// already redirected whatever it carried.
			continue
		}
		switch {
		case f.Type.Union != nil:
			m.materializeUnion(f, v, values, rep)
		case f.Type.Object != nil:
			// A [ScalarOr] field written as a scalar carries a value of its
			// own, which stands for the whole object.
			if a, ok := values[f.Path]; ok && a.State == ValuePresent {
				m.materializeShorthand(f, v, a, rep)
				continue
			}
			m.materialize(f.Type.Object, v.FieldByIndex(f.GoPath.Index), values, rep)
		default:
			m.materializeLeaf(f, v, values, rep)
		}
	}
}

func (m *Model) materializeUnion(f *FieldModel, v reflect.Value, values map[string]Assignment, rep *Report) {
	path, _ := DiscriminatorPath(f)
	a, ok := values[path]
	if !ok || a.State != ValuePresent {
		rep.Diagnostics.errorf(CodeMissingDefinition, f.GoName, path,
			"union %q requires discriminator %q", f.Path, f.Type.Union.Discriminator)
		return
	}

	tag := fmt.Sprint(a.Value)
	for _, variant := range f.Type.Union.Variants {
		if variant.Tag != tag {
			continue
		}
		fv := v.FieldByIndex(variant.acc.index)
		nv := reflect.New(fv.Type().Elem())
		fv.Set(nv)
		rep.origins[path] = a.Origin
		m.materialize(variant.Object, nv.Elem(), values, rep)
		return
	}

	tags := make([]string, len(f.Type.Union.Variants))
	for i, variant := range f.Type.Union.Variants {
		tags[i] = variant.Tag
	}
	rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
		Severity:  SeverityError,
		Code:      CodeUnionInvalid,
		FieldPath: path,
		GoPath:    f.GoName,
		Message:   fmt.Sprintf("unknown variant %q, want one of [%s]", tag, strings.Join(tags, ", ")),
		Origin:    &a.Origin,
	})
}

// materializeShorthand widens the scalar spelling of a [ScalarOr] field and
// assigns the whole object.
func (m *Model) materializeShorthand(f *FieldModel, v reflect.Value, a Assignment, rep *Report) {
	if f.widen == nil {
		rep.diag(f, &a.Origin, CodeSourceUnsupported, "cannot assign a value to an object")
		return
	}
	value, err := f.widen(a.Value)
	if err != nil {
		rep.diag(f, &a.Origin, CodeConstraintMismatch, Redact(f, err.Error(), a.Value))
		return
	}
	if err := f.acc.set(v, value); err != nil {
		rep.diag(f, &a.Origin, CodeConstraintMismatch, Redact(f, err.Error(), a.Value))
		return
	}
	rep.origins[f.Path] = a.Origin
	if f.Meta.Secret {
		rep.secrets[f.Path] = struct{}{}
	}
}

func (m *Model) materializeLeaf(f *FieldModel, v reflect.Value, values map[string]Assignment, rep *Report) {
	a, ok := values[f.Path]
	if !ok || a.State == ValueMissing {
		m.applyDefault(f, v, rep)
		return
	}

	value, err := convertValue(f.Type.Go, a.Value)
	if err != nil {
		rep.diag(f, &a.Origin, CodeConstraintMismatch, Redact(f, err.Error(), a.Value))
		return
	}
	if err := f.Validate(value); err != nil {
		rep.diag(f, &a.Origin, CodeConstraintMismatch, Redact(f, err.Error(), value, a.Value))
		return
	}
	if err := f.acc.set(v, value); err != nil {
		rep.diag(f, &a.Origin, CodeConstraintMismatch, Redact(f, err.Error(), value, a.Value))
		return
	}
	rep.origins[f.Path] = a.Origin
	if f.Meta.Secret {
		rep.secrets[f.Path] = struct{}{}
	}

	// Deprecation is worth nothing to an operator unless using the key says so.
	if f.Meta.Deprecated != "" {
		origin := a.Origin
		rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
			Severity:  SeverityWarning,
			Code:      CodeDeprecated,
			Message:   f.Meta.Deprecated,
			FieldPath: f.Path,
			GoPath:    f.GoName,
			Origin:    &origin,
		})
	}
}

func (m *Model) applyDefault(f *FieldModel, v reflect.Value, rep *Report) {
	if f.Default != nil && f.Default.Applied {
		value, err := convertValue(f.Type.Go, f.Default.Value)
		if err != nil {
			rep.diag(f, nil, CodeDefaultMismatch, err.Error())
			return
		}
		if err := f.acc.set(v, value); err != nil {
			rep.diag(f, nil, CodeDefaultMismatch, err.Error())
			return
		}
		rep.origins[f.Path] = Origin{Source: "default"}
		if f.Meta.Secret {
			rep.secrets[f.Path] = struct{}{}
		}
		return
	}
	if f.Presence == PresenceRequired {
		rep.diag(f, nil, CodeMissingDefinition, "no value provided and no default")
	}
}

func (r *Report) diag(f *FieldModel, origin *Origin, code, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity:  SeverityError,
		Code:      code,
		Message:   msg,
		FieldPath: f.Path,
		GoPath:    f.GoName,
		Origin:    origin,
	})
}

// convertValue coerces a decoded value to the Go type of the field.
func convertValue(want reflect.Type, v any) (any, error) {
	if v == nil {
		return nil, errors.Errorf("cannot assign nil to %s", want)
	}
	rv := reflect.ValueOf(v)
	switch {
	case rv.Type() == want:
		return v, nil
	case rv.Type().AssignableTo(want):
		return rv.Convert(want).Interface(), nil
	case rv.Type().ConvertibleTo(want) && convertSafe(rv.Type(), want):
		return rv.Convert(want).Interface(), nil
	default:
		return nil, errors.Errorf("cannot use %s as %s", rv.Type(), want)
	}
}

// convertSafe rejects conversions that silently change meaning, such as
// int-to-string, which Go permits but configuration never wants.
func convertSafe(from, to reflect.Type) bool {
	if to.Kind() == reflect.String && from.Kind() != reflect.String {
		return false
	}
	if from.Kind() == reflect.String && to.Kind() != reflect.String {
		return false
	}
	return true
}
