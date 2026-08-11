// Package docs emits reference documentation from a configuration descriptor,
// so that the documented configuration cannot drift from the decoded one.
//
// Everything the page shows is already in the compiled model: paths, types,
// presence, defaults, constraints and documentation. Names are not, because a
// name belongs to a source rather than to the model; see [ForSource], which
// asks the source itself.
//
// [Generate] renders Markdown. [Build] returns the [Page] it renders, for a
// caller that wants another format.
package docs

import (
	"github.com/go-faster/figureout"
)

// Target identifies the documentation target.
const Target = figureout.TargetID("docs")

// Diagnostic codes reported by generation.
const (
	// CodeNotDocumentable reports a rule that has no prose form, so that
	// generation never silently implies the page is exhaustive.
	CodeNotDocumentable = "target.not_documentable"
	// CodeSourceNotNamed reports a source that cannot say which names it
	// accepts, whose column is therefore omitted.
	CodeSourceNotNamed = "target.source_not_named"
)

// Option customizes generation.
type Option interface {
	applyDocs(*generator) error
}

type optionFunc func(*generator) error

func (f optionFunc) applyDocs(g *generator) error { return f(g) }

// Title sets the page title.
func Title(title string) Option {
	return optionFunc(func(g *generator) error {
		g.title = title
		return nil
	})
}

// ForSource documents the names one source accepts, as a column of its own.
//
// It takes a configured source rather than a [figureout.SourceID] because the
// names depend on that configuration: env.Current(env.Prefix("APP_")) reads
// APP_SERVER_PORT where an unprefixed source reads SERVER_PORT. A source that
// does not implement [figureout.SourceNamer] is reported and omitted.
func ForSource(s figureout.Source) Option {
	return optionFunc(func(g *generator) error {
		namer, ok := s.(figureout.SourceNamer)
		if !ok {
			g.unnamed = append(g.unnamed, s.ID())
			return nil
		}
		g.sources = append(g.sources, &Source{ID: s.ID(), names: namer.ProjectNames(g.model)})
		return nil
	})
}

// StrictExport turns "cannot be documented" reports into errors.
func StrictExport() Option {
	return optionFunc(func(g *generator) error {
		g.strict = true
		return nil
	})
}

// Build compiles the documentation model for the descriptor.
func Build[T any](d *figureout.Descriptor[T], opts ...Option) (*Page, figureout.Diagnostics, error) {
	g := &generator{model: d.Model()}
	for _, o := range opts {
		if err := o.applyDocs(g); err != nil {
			return nil, nil, err
		}
	}
	for _, id := range g.unnamed {
		g.diags = append(g.diags, figureout.Diagnostic{
			Severity: figureout.SeverityWarning,
			Code:     CodeSourceNotNamed,
			Target:   Target,
			Source:   id,
			Message:  "source cannot report the names it accepts and is not documented",
		})
	}

	page := g.page()
	if g.strict {
		for i := range g.diags {
			g.diags[i].Severity = figureout.SeverityError
		}
	}
	if err := g.diags.Err(); err != nil {
		return nil, g.diags, err
	}
	return page, g.diags, nil
}

// Generate renders Markdown reference documentation for the descriptor.
func Generate[T any](d *figureout.Descriptor[T], opts ...Option) ([]byte, figureout.Diagnostics, error) {
	page, diags, err := Build(d, opts...)
	if err != nil {
		return nil, diags, err
	}
	return page.Markdown(), diags, nil
}
