package figureout

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"unsafe"

	"github.com/go-faster/errors"
)

// CompletenessMode selects which Go fields must be accounted for.
type CompletenessMode uint8

// Completeness modes.
const (
	// CompletenessExported requires every exported field to be registered,
	// delegated, covered by registered descendants, or ignored.
	CompletenessExported CompletenessMode = iota
	// CompletenessStrict additionally requires unexported fields to be
	// explicitly ignored.
	CompletenessStrict
	// CompletenessTagged considers only fields carrying the configured tag.
	CompletenessTagged
	// CompletenessDisabled performs no completeness validation.
	CompletenessDisabled
)

// SchemaOption customizes descriptor construction.
type SchemaOption interface {
	applySchema(*schemaOptions) error
}

type schemaOptionFunc func(*schemaOptions) error

func (f schemaOptionFunc) applySchema(o *schemaOptions) error { return f(o) }

type schemaOptions struct {
	completeness CompletenessMode
	tag          string
	registry     *TypeRegistry
}

// Completeness selects the completeness mode. The default is
// [CompletenessExported].
func Completeness(m CompletenessMode) SchemaOption {
	return schemaOptionFunc(func(o *schemaOptions) error {
		o.completeness = m
		return nil
	})
}

// Tag sets the struct tag consulted by [CompletenessTagged]. The default is
// "config".
func Tag(name string) SchemaOption {
	return schemaOptionFunc(func(o *schemaOptions) error {
		if name == "" {
			return errors.New("empty tag name")
		}
		o.tag = name
		return nil
	})
}

// WithTypeRegistry supplies named type descriptions to descriptor construction.
func WithTypeRegistry(r *TypeRegistry) SchemaOption {
	return schemaOptionFunc(func(o *schemaOptions) error {
		o.registry = r
		return nil
	})
}

type regKind uint8

const (
	regField regKind = iota
	regObject
	regUnion
	regIgnore
	regGroup
)

// registration is one recorded declaration, before compilation.
type registration struct {
	kind regKind
	// valid is false once the declaration has been reported as broken; the
	// fluent builder then turns into a no-op instead of panicking.
	valid  bool
	name   string
	goName string
	bound  bound

	typ Type
	acc accessor

	meta        Metadata
	def         *Default
	required    bool
	zeroDefault bool
	merge       MergePolicy
	mergeKey    string
	movedFrom   []string
	constraints []Constraint
	sources     map[SourceID]*SourceProjection
	targets     map[TargetID][]any

	// Ignore.
	recursive bool
	reason    string

	// Object, union and group payloads.
	object *ObjectModel
	union  *Union
	group  *container
	widen  func(any) (any, error)
}

// container is one level of the configuration path.
//
// Every registration in a container binds a Go field of the same struct: a
// [Group] opens a path level without opening a Go one, so its members stay
// relative to the builder root and the synthetic object field that carries them
// has an empty Go index.
type container struct {
	regs []*registration
}

// builder accumulates registrations for one struct.
//
// A nested [ObjectFunc] gets its own builder rooted at the nested value, so
// pointer binding, completeness and name collisions are all scoped to the
// struct that declares them, exactly as they are for a separate [Derive].
type builder struct {
	rootType reflect.Type
	root     reflect.Value
	goPath   string
	bind     *binder
	// stack is the open containers, outermost first. It always holds at least
	// the root container.
	stack      []*container
	invariants []invariant
	diags      Diagnostics
	opts       schemaOptions
}

// newBuilder starts a builder over an addressable struct value. goPath prefixes
// the Go paths reported in diagnostics.
func newBuilder(root reflect.Value, goPath string, opts schemaOptions) *builder {
	return &builder{
		rootType: root.Type(),
		root:     root,
		goPath:   goPath,
		bind:     newBinder(root, goPath),
		stack:    []*container{{}},
		opts:     opts,
	}
}

// add records a declaration in the innermost open container.
func (b *builder) add(reg *registration) {
	c := b.stack[len(b.stack)-1]
	c.regs = append(c.regs, reg)
}

// Schema is the mutable registration builder for T.
//
// It is single-use and not safe for concurrent use; the compiled [Descriptor]
// is immutable and safe for concurrent use.
type Schema[T any] struct {
	b *builder
}

// Diagnostics returns the diagnostics recorded so far.
func (s *Schema[T]) Diagnostics() Diagnostics { return s.b.diags }

// Derive compiles a descriptor for T.
//
// It allocates a synthetic zero value of T, passes its address to describe,
// resolves every registered pointer to a Go field, validates completeness and
// consistency, and compiles an immutable descriptor. The runtime values of the
// synthetic object are never used as defaults.
func Derive[T any](describe func(*T, *Schema[T]), opts ...SchemaOption) (*Descriptor[T], error) {
	if describe == nil {
		return nil, errors.New("nil describe function")
	}

	options := schemaOptions{tag: "config"}
	for _, o := range opts {
		if err := o.applySchema(&options); err != nil {
			return nil, errors.Wrap(err, "schema option")
		}
	}

	root := new(T)
	rv := reflect.ValueOf(root).Elem()
	if rv.Kind() != reflect.Struct {
		return nil, errors.Errorf("configuration type must be a struct, got %s", rv.Type())
	}

	b := newBuilder(rv, rv.Type().Name(), options)
	describe(root, &Schema[T]{b: b})
	runtime.KeepAlive(root)

	obj := b.compile()
	if err := b.diags.Err(); err != nil {
		return nil, err
	}

	model := &Model{Root: obj}
	for _, inv := range b.invariants {
		model.invariants = append(model.invariants, InvariantModel{Name: inv.name})
	}
	prefixPaths(obj, "")
	model.reindex()
	return &Descriptor[T]{model: model, invariants: b.invariants}, nil
}

// MustDerive is like [Derive] but panics on error.
//
// The panic message contains every compilation diagnostic, not only the first.
//
// A failed derivation is a programming error, so a package-level
// "var ConfigDescriptor = figureout.MustDerive(...)" is the intended idiom for
// a library. A binary that would rather report the failure than crash in init
// should derive inside its own loader instead:
//
//	var descriptor = sync.OnceValues(func() (*figureout.Descriptor[Config], error) {
//		return figureout.Derive(describe)
//	})
func MustDerive[T any](describe func(*T, *Schema[T]), opts ...SchemaOption) *Descriptor[T] {
	d, err := Derive(describe, opts...)
	if err != nil {
		panic("figureout: " + err.Error())
	}
	return d
}

// register resolves a field pointer and records a declaration.
func (b *builder) register(ptr unsafe.Pointer, carrier reflect.Type, name string, kind regKind) *registration {
	reg := &registration{kind: kind, name: name}

	bd, err := b.bind.resolve(ptr, carrier, name)
	if err != nil {
		var d *Diagnostic
		if errors.As(err, &d) {
			b.diags = append(b.diags, *d)
		} else {
			b.diags.errorf(CodeForeignPointer, "", name, "%s", err)
		}
		return reg
	}

	reg.bound = bd
	reg.goName = bd.goPath
	reg.acc = accessor{
		index:    bd.index,
		settable: !bd.skipped,
	}
	presence, elem := unwrapCarrier(bd.typ)
	reg.acc.presence = presence
	reg.acc.elem = elem

	if kind == regIgnore {
		reg.valid = true
		b.add(reg)
		return reg
	}

	if name == "" {
		b.diags.errorf(CodeMissingDefinition, bd.goPath, "", "empty configuration name")
		return reg
	}

	// Two carriers stacked are two answers to one question: which of the two
	// nils means the value is missing has no defensible answer.
	if presence == PresencePointer {
		if inner, _ := unwrapCarrier(elem); inner != PresenceRequired {
			b.diags.errorf(CodeUnsupportedType, bd.goPath, name,
				"%s is a %s carrier behind a pointer; absence has to be spelled once",
				bd.goPath, inner)
			return reg
		}
	}

	typ, ok := b.deriveType(elem)
	if !ok {
		b.diags.errorf(CodeUnsupportedType, bd.goPath, name,
			"cannot derive a semantic type for %s", elem)
		return reg
	}
	reg.typ = typ

	if r := b.opts.registry; r != nil {
		r.apply(reg)
	}

	reg.valid = true
	b.add(reg)
	return reg
}

func (b *builder) deriveType(elem reflect.Type) (Type, bool) {
	if r := b.opts.registry; r != nil {
		if t, ok := r.lookup(elem); ok {
			return t, true
		}
	}
	return deriveType(elem)
}

// applyOptions runs field options against a registration.
func (b *builder) applyOptions(reg *registration, opts []FieldOption) {
	ctx := &fieldContext{reg: reg}
	for _, o := range opts {
		if o == nil {
			continue
		}
		if err := o.ApplyFieldOption(ctx); err != nil {
			b.diags.errorf(CodeMissingDefinition, reg.goName, reg.name, "option: %s", err)
		}
	}
}

// compile runs the validation phases and produces the object model for the
// struct this builder describes. Diagnostics accumulate on the builder.
func (b *builder) compile() *ObjectModel {
	handled := map[string]*registration{}
	root := b.compileContainer(b.stack[0], handled)
	b.checkCompleteness(handled)
	b.placeMoved(root)
	return root
}

// compileContainer turns one path level into an object model. handled is shared
// across the whole builder, because completeness and duplicate-registration
// checks are about Go fields, which a group does not nest.
func (b *builder) compileContainer(c *container, handled map[string]*registration) *ObjectModel {
	root := &ObjectModel{Go: b.rootType}
	byName := map[string]*registration{}

	for _, reg := range c.regs {
		if !reg.valid {
			continue // already reported
		}
		if reg.kind == regGroup {
			if prev, ok := byName[reg.name]; ok {
				b.diags.errorf(CodeDuplicateName, reg.goName, reg.name,
					"configuration property %q is bound to both %s and a group", reg.name, prev.goName)
				continue
			}
			byName[reg.name] = reg
			root.Fields = append(root.Fields, &FieldModel{
				Name:     reg.name,
				Path:     reg.name,
				GoName:   b.goPath,
				Presence: PresenceRequired,
				Meta:     reg.meta,
				Targets:  reg.targets,
				Sources:  reg.sources,
				Type: Type{
					Kind:   TypeObject,
					Go:     b.rootType,
					Object: b.compileContainer(reg.group, handled),
				},
			})
			continue
		}
		if prev, ok := handled[reg.goName]; ok {
			b.diags.errorf(CodeDuplicateField, reg.goName, reg.name,
				"%s is registered more than once: %q and %q", reg.goName, prev.name, reg.name)
			continue
		}
		handled[reg.goName] = reg

		if reg.kind == regIgnore {
			continue
		}
		if prev, ok := byName[reg.name]; ok {
			b.diags.errorf(CodeDuplicateName, reg.goName, reg.name,
				"configuration property %q is bound to both %s and %s", reg.name, prev.goName, reg.goName)
			continue
		}
		byName[reg.name] = reg

		f := &FieldModel{
			Name:        reg.name,
			Path:        reg.name,
			GoPath:      FieldPath{Index: reg.bound.index, Offset: reg.bound.offset},
			GoName:      reg.goName,
			Type:        reg.typ,
			Presence:    reg.acc.presence,
			Meta:        reg.meta,
			Default:     reg.def,
			Merge:       reg.merge,
			Constraints: reg.constraints,
			Sources:     reg.sources,
			Targets:     reg.targets,
			MovedFrom:   reg.movedFrom,
			required:    reg.required,
			zeroDefault: reg.zeroDefault,
			widen:       reg.widen,
			acc:         reg.acc,
		}
		if reg.object != nil {
			f.Type.Object = reg.object
		}
		if reg.union != nil {
			f.Type.Union = reg.union
		}
		if reg.mergeKey != "" {
			b.resolveMergeKey(f, reg.mergeKey)
		}
		b.validateField(f)
		root.Fields = append(root.Fields, f)
	}
	return root
}

// prefixPaths assigns canonical dotted paths to nested fields.
func prefixPaths(o *ObjectModel, prefix string) {
	for _, f := range o.Fields {
		f.Path = prefix + f.Name
		if f.movedTo != nil {
			// A shadow carries its target's models by pointer. Walking them
			// here would re-path the target's own members under the former
			// name, which is the field's real description.
			continue
		}
		switch {
		case f.Type.Object != nil:
			prefixPaths(f.Type.Object, f.Path+".")
		case f.Type.Union != nil:
			for _, v := range f.Type.Union.Variants {
				prefixPaths(v.Object, f.Path+".")
			}
		}
		if elem, ok := collectionOf(f); ok {
			// Every element shares one description, so the model spells the
			// subscript empty: "sites[].max_bytes".
			prefixPaths(elem, ElementPath(f.Path, "")+".")
		}
	}
}

// validateField checks constraints and defaults against the semantic type.
func (b *builder) validateField(f *FieldModel) {
	for _, c := range f.Constraints {
		if !c.Applies(f.Type.Kind) {
			b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
				"constraint %q does not apply to a %s field", c.Kind(), f.Type.Kind)
		}
	}
	// A fallback the field itself rejects is a descriptor that cannot resolve,
	// so it fails where the declaration is rather than where the configuration
	// is missing.
	if fb, ok := fallback(f); ok {
		if err := f.Validate(fb.value); err != nil {
			b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
				"%s does not satisfy the field's own constraints (%s); %s",
				fb.what, err, fb.advice)
		}
	}
	if f.Default != nil && f.Default.Value != nil {
		dt := reflect.TypeOf(f.Default.Value)
		if !dt.AssignableTo(f.Type.Go) && !dt.ConvertibleTo(f.Type.Go) {
			b.diags.errorf(CodeDefaultMismatch, f.GoName, f.Name,
				"default of type %s is not assignable to %s", dt, f.Type.Go)
		}
	}
	// Deriving clean and failing at resolve time is the wrong end to fail at: a
	// descriptor that cannot possibly work should not compile. A field that
	// installed its own decoder owns its shape, so it describes itself.
	if !decoded(f) {
		if elem := f.Type.Elem; elem != nil && elem.Kind == TypeObject && elem.Object == nil {
			b.diags.errorf(CodeMissingDefinition, f.GoName, f.Name,
				"elements of %q are objects with no description; register it with %s",
				f.Name, collectionRegistrars(f.Type.Kind))
		}
		if f.Type.Kind == TypeObject && f.Type.Object == nil && f.Type.Union == nil {
			b.diags.errorf(CodeMissingDefinition, f.GoName, f.Name,
				"%q is an object with no description; register it with Object or ObjectFunc", f.Name)
		}
	}
	// A keyed list merges by key exactly as a map does; without a key, by-key
	// has nothing to identify an element with.
	keyed := f.Merge == MergeByKey && f.Type.Kind == TypeList && f.mergeKey != nil
	if !keyed && !f.Merge.Applies(f.Type.Kind) {
		b.diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"merge policy %q does not apply to a %s field", f.Merge, f.Type.Kind)
	}
	if !f.acc.settable {
		b.diags.errorf(CodeMissingDefinition, f.GoName, f.Name,
			"%s is unexported and cannot be assigned; ignore it instead", f.GoName)
	}
}

// absentValue is what a field resolves to when no source provides a value,
// together with how a descriptor says that absence is an error instead.
type absentValue struct {
	value  any
	what   string
	advice string
}

// fallback returns the value absence resolves to, if it resolves to one at all.
//
// It mirrors what [Model.applyDefault] does at resolution: a collection is
// empty, a [Value] field is zero, and everything else is either required or
// carries its own default.
func fallback(f *FieldModel) (absentValue, bool) {
	if f.Presence != PresenceRequired || f.Required() {
		return absentValue{}, false
	}
	if f.Default != nil && f.Default.Applied {
		return absentValue{}, false
	}
	if empty, ok := emptyCollection(f.Type); ok {
		return absentValue{
			value:  empty,
			what:   fmt.Sprintf("an empty %s", f.Type.Kind),
			advice: "register it with Explicit, or mark it Required, so absence is an error",
		}, true
	}
	if f.ZeroDefault() {
		return absentValue{
			value:  reflect.New(f.Type.Go).Elem().Interface(),
			what:   fmt.Sprintf("the zero value of %s", f.Type.Go),
			advice: "register it with Explicit, or give it a default",
		}, true
	}
	return absentValue{}, false
}

// decoded reports whether any source decodes the field itself, in which case
// the shape it accepts is the decoder's business rather than the model's.
func decoded(f *FieldModel) bool {
	for _, p := range f.Sources {
		if p.Decoder != nil {
			return true
		}
	}
	return false
}

func collectionRegistrars(k TypeKind) string {
	if k == TypeMap {
		return "MapOf or Map"
	}
	return "ListOf or List"
}

// checkCompleteness verifies that every eligible field is accounted for.
func (b *builder) checkCompleteness(handled map[string]*registration) {
	if b.opts.completeness == CompletenessDisabled {
		return
	}

	// Recursive ignores cover their whole subtree.
	var ignoredTrees []string
	for path, reg := range handled {
		if reg.kind == regIgnore && reg.recursive {
			ignoredTrees = append(ignoredTrees, path+".")
		}
	}
	slices.Sort(ignoredTrees)

	covered := func(path string) bool {
		if _, ok := handled[path]; ok {
			return true
		}
		for _, tree := range ignoredTrees {
			if strings.HasPrefix(path, tree) {
				return true
			}
		}
		return false
	}

	var check func(fields []bound)
	check = func(fields []bound) {
		for _, f := range fields {
			if !b.eligible(f) || covered(f.goPath) {
				continue
			}
			if f.typ.Kind() == reflect.Struct && !isOpaqueStruct(f.typ) {
				check(b.bind.children(f.index))
				continue
			}
			b.diags.errorf(CodeMissingDefinition, f.goPath, "",
				"%s is neither registered nor explicitly ignored", f.goPath)
		}
	}
	check(b.bind.children(nil))
}

func (b *builder) eligible(f bound) bool {
	switch b.opts.completeness {
	case CompletenessStrict:
		return true
	case CompletenessTagged:
		sf, ok := b.fieldByIndex(f.index)
		if !ok {
			return false
		}
		_, tagged := sf.Tag.Lookup(b.opts.tag)
		return tagged
	default:
		return !f.skipped
	}
}

func (b *builder) fieldByIndex(index []int) (reflect.StructField, bool) {
	t := b.rootType
	var sf reflect.StructField
	for i, idx := range index {
		if t.Kind() != reflect.Struct || idx >= t.NumField() {
			return reflect.StructField{}, false
		}
		sf = t.Field(idx)
		if i < len(index)-1 {
			t = sf.Type
		}
	}
	return sf, true
}
