package figureout

import (
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
)

// registration is one recorded declaration, before compilation.
type registration struct {
	kind   regKind
	name   string
	goName string
	bound  bound

	typ Type
	acc accessor

	meta        Metadata
	def         *Default
	merge       MergePolicy
	constraints []Constraint
	sources     map[SourceID]*SourceProjection
	targets     map[TargetID][]any

	// Ignore.
	recursive bool
	reason    string

	// Object and union payloads.
	object *ObjectModel
	union  *Union
}

// builder accumulates registrations for one descriptor.
type builder struct {
	rootType reflect.Type
	root     reflect.Value
	bind     *binder
	regs     []*registration
	diags    Diagnostics
	opts     schemaOptions
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

	b := &builder{
		rootType: rv.Type(),
		root:     rv,
		bind:     newBinder(rv),
		opts:     options,
	}
	describe(root, &Schema[T]{b: b})
	runtime.KeepAlive(root)

	model, diags := b.compile()
	if err := diags.Err(); err != nil {
		return nil, err
	}
	return &Descriptor[T]{model: model}, nil
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
		b.regs = append(b.regs, reg)
		return reg
	}

	if name == "" {
		b.diags.errorf(CodeMissingDefinition, bd.goPath, "", "empty configuration name")
		return reg
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

	b.regs = append(b.regs, reg)
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

// compile runs the validation phases and produces the model.
func (b *builder) compile() (*Model, Diagnostics) {
	diags := b.diags

	root := &ObjectModel{Go: b.rootType}
	handled := map[string]*registration{}
	byName := map[string]*registration{}

	for _, reg := range b.regs {
		if reg.goName == "" {
			continue // already reported
		}
		if prev, ok := handled[reg.goName]; ok {
			diags.errorf(CodeDuplicateField, reg.goName, reg.name,
				"%s is registered more than once: %q and %q", reg.goName, prev.name, reg.name)
			continue
		}
		handled[reg.goName] = reg

		if reg.kind == regIgnore {
			continue
		}
		if prev, ok := byName[reg.name]; ok {
			diags.errorf(CodeDuplicateName, reg.goName, reg.name,
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
			acc:         reg.acc,
		}
		if reg.object != nil {
			f.Type.Object = reg.object
		}
		if reg.union != nil {
			f.Type.Union = reg.union
		}
		b.validateField(f, &diags)
		root.Fields = append(root.Fields, f)
	}

	b.checkCompleteness(handled, &diags)

	model := &Model{Root: root}
	prefixPaths(root, "")
	model.reindex()
	return model, diags
}

// prefixPaths assigns canonical dotted paths to nested fields.
func prefixPaths(o *ObjectModel, prefix string) {
	for _, f := range o.Fields {
		f.Path = prefix + f.Name
		switch {
		case f.Type.Object != nil:
			prefixPaths(f.Type.Object, f.Path+".")
		case f.Type.Union != nil:
			for _, v := range f.Type.Union.Variants {
				prefixPaths(v.Object, f.Path+".")
			}
		}
	}
}

// validateField checks constraints and defaults against the semantic type.
func (b *builder) validateField(f *FieldModel, diags *Diagnostics) {
	for _, c := range f.Constraints {
		if !c.Applies(f.Type.Kind) {
			diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
				"constraint %q does not apply to a %s field", c.Kind(), f.Type.Kind)
		}
	}
	if f.Default != nil && f.Default.Value != nil {
		dt := reflect.TypeOf(f.Default.Value)
		if !dt.AssignableTo(f.Type.Go) && !dt.ConvertibleTo(f.Type.Go) {
			diags.errorf(CodeDefaultMismatch, f.GoName, f.Name,
				"default of type %s is not assignable to %s", dt, f.Type.Go)
		}
	}
	if !f.Merge.Applies(f.Type.Kind) {
		diags.errorf(CodeConstraintMismatch, f.GoName, f.Name,
			"merge policy %q does not apply to a %s field", f.Merge, f.Type.Kind)
	}
	if !f.acc.settable {
		diags.errorf(CodeMissingDefinition, f.GoName, f.Name,
			"%s is unexported and cannot be assigned; ignore it instead", f.GoName)
	}
}

// checkCompleteness verifies that every eligible field is accounted for.
func (b *builder) checkCompleteness(handled map[string]*registration, diags *Diagnostics) {
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
			diags.errorf(CodeMissingDefinition, f.goPath, "",
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
