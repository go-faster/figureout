package figureout

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Invariant registers a rule that spans fields, checked once the configuration
// has resolved.
//
// Constraints are per field, and real configurations are full of rules that are
// not: a key that must name an entry in another map, a flag that only takes
// effect when a credential is set, a lease that must outlast a timeout. Without
// somewhere to put them they become a hand-written pass after Resolve, which
// loses both the schema and the provenance the descriptor already has.
//
//	figureout.Invariant(s, "proxy-exists", func(c *Config) error {
//		for i, site := range c.Fetch.Sites {
//			if _, ok := c.Proxies[site.Proxy]; !ok {
//				return figureout.At(fmt.Sprintf("fetch.sites[%d].proxy", i)).
//					Errorf("no proxy named %q is configured", site.Proxy)
//			}
//		}
//		return nil
//	})
//
// Invariants run only after every field resolved and validated, so a failure is
// never a consequence of an error already reported. Returning [At] keeps the
// origin of the offending value; a plain error is reported without one. Return
// several with [errors.Join].
//
// Registered on a nested [ObjectFunc] schema, an invariant sees the nested
// struct and its violation paths are prefixed with the nested object's path.
func Invariant[T any](s *Schema[T], name string, check func(*T) error) {
	b := s.b
	switch {
	case name == "":
		b.diags.errorf(CodeMissingDefinition, b.goPath, "", "empty invariant name")
		return
	case check == nil:
		b.diags.errorf(CodeMissingDefinition, b.goPath, "", "invariant %q has no check", name)
		return
	}
	if slices.ContainsFunc(b.invariants, func(i invariant) bool { return i.name == name }) {
		b.diags.errorf(CodeDuplicateName, b.goPath, "", "invariant %q is registered more than once", name)
		return
	}

	b.invariants = append(b.invariants, invariant{
		name: name,
		check: func(rv reflect.Value) error {
			return check(rv.Addr().Interface().(*T))
		},
	})
}

// invariant is one compiled cross-field rule.
type invariant struct {
	name string
	// prefix qualifies violation paths reported by a nested schema.
	prefix string
	check  func(root reflect.Value) error
}

// InvariantModel names a cross-field rule in the compiled model.
//
// Only the name is format-neutral: the rule itself is a Go function, so no
// target can emit it. Documentation generators list it so that a reader knows a
// rule exists which the schema does not describe.
type InvariantModel struct {
	Name string
}

// Invariants returns the cross-field rules the descriptor declares.
func (m *Model) Invariants() []InvariantModel { return m.invariants }

// lift re-roots a nested schema's invariants onto the parent.
func lift(invariants []invariant, name string, acc accessor) []invariant {
	out := make([]invariant, 0, len(invariants))
	for _, inv := range invariants {
		out = append(out, invariant{
			name:   name + "." + inv.name,
			prefix: joinPath(name, inv.prefix),
			check: func(rv reflect.Value) error {
				fv, ok := acc.reach(rv)
				if !ok {
					// A rule about the members of a section nobody wrote has
					// nothing to be violated by.
					return nil
				}
				return inv.check(fv)
			},
		})
	}
	return out
}

func joinPath(prefix, rest string) string {
	switch {
	case prefix == "":
		return rest
	case rest == "":
		return prefix
	default:
		return prefix + "." + rest
	}
}

// At starts a violation about the values at one or more canonical paths.
//
// The paths are what give a cross-field failure the same provenance a
// constraint failure has: the report already knows that "fetch.sites[0].proxy"
// came from config.yaml:41:5.
func At(paths ...string) *Violation { return &Violation{paths: paths} }

// Violation builds an error naming the paths a cross-field rule is about.
type Violation struct{ paths []string }

// Errorf returns the violation as an error.
func (v *Violation) Errorf(format string, args ...any) error {
	return &violationError{paths: v.paths, message: fmt.Sprintf(format, args...)}
}

type violationError struct {
	paths   []string
	message string
}

func (e *violationError) Error() string {
	if len(e.paths) == 0 {
		return e.message
	}
	return strings.Join(e.paths, ", ") + ": " + e.message
}

// checkInvariants runs every rule against a resolved configuration.
func (d *Descriptor[T]) checkInvariants(rv reflect.Value, rep *Report) {
	for _, inv := range d.invariants {
		err := inv.check(rv)
		if err == nil {
			continue
		}
		for _, leaf := range flatten(err) {
			rep.Diagnostics = append(rep.Diagnostics, d.violation(inv, leaf, rep))
		}
	}
}

func (d *Descriptor[T]) violation(inv invariant, err error, rep *Report) Diagnostic {
	diag := Diagnostic{
		Severity: SeverityError,
		Code:     CodeInvariantViolated,
		Message:  err.Error(),
	}

	v, ok := err.(*violationError)
	if !ok || len(v.paths) == 0 {
		return diag
	}

	paths := make([]string, len(v.paths))
	for i, p := range v.paths {
		paths[i] = joinPath(inv.prefix, p)
	}
	diag.Message = v.message
	diag.FieldPath = paths[0]
	if len(paths) > 1 {
		diag.Message += " (see also " + strings.Join(paths[1:], ", ") + ")"
	}
	// A violation may name an element rather than a field, as
	// "fetch.sites[0].proxy" does. The report knows origins per field, so the
	// nearest enclosing one is the closest true answer.
	if field := nearest(d.model, paths[0]); field != "" {
		if f, ok := d.model.FieldByPath(field); ok {
			diag.GoPath = f.GoName
		}
		if origin, ok := rep.OriginOf(field); ok {
			diag.Origin = &origin
		}
	}
	return diag
}

// nearest returns the longest model path that is a prefix of path, or "".
func nearest(m *Model, path string) string {
	for path != "" {
		if _, ok := m.FieldByPath(path); ok {
			return path
		}
		if i := strings.LastIndexAny(path, ".["); i >= 0 {
			path = path[:i]
			continue
		}
		return ""
	}
	return ""
}

// flatten expands an [errors.Join] tree into the errors it holds, so returning
// several violations at once reports several diagnostics.
func flatten(err error) []error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range joined.Unwrap() {
		out = append(out, flatten(e)...)
	}
	return out
}
