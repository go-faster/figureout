package docs

import (
	"strings"

	"github.com/go-faster/figureout"
)

// column is one table column, present only when some row fills it.
type column struct {
	header string
	cell   func(f *Field) string
}

// Markdown renders the page as a Markdown document.
//
// Every table names only the columns its own object fills, so an object
// without enumerated values or constraints stays narrow enough to read.
func (p *Page) Markdown() []byte {
	var sb strings.Builder
	if p.Title != "" {
		sb.WriteString("# " + p.Title + "\n\n")
	}
	for i, s := range p.Sections {
		if i > 0 {
			sb.WriteString("\n")
		}
		p.section(&sb, s)
	}
	return []byte(sb.String())
}

func (p *Page) section(sb *strings.Builder, s *Section) {
	sb.WriteString("## " + s.Title + "\n\n")
	if s.Doc != "" {
		sb.WriteString(s.Doc + "\n\n")
	}
	if s.Deprecated != "" {
		sb.WriteString("**Deprecated.** " + s.Deprecated + "\n\n")
	}
	if s.Variant != "" {
		sb.WriteString("Selected by `" + s.Discriminator + ": " + s.Variant + "`.\n\n")
	}
	if len(s.Fields) == 0 {
		sb.WriteString("No configurable fields.\n")
		return
	}

	cols := p.columns(s)
	sb.WriteString(row(headers(cols)))
	sb.WriteString(divider(len(cols)))
	for _, f := range s.Fields {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = escape(c.cell(f))
		}
		sb.WriteString(row(cells))
	}

	p.examples(sb, s)
}

// examples renders example values as fenced blocks, which a table cell cannot
// hold.
func (p *Page) examples(sb *strings.Builder, s *Section) {
	var with []*Field
	for _, f := range s.Fields {
		if len(f.Examples) > 0 {
			with = append(with, f)
		}
	}
	if len(with) == 0 {
		return
	}

	sb.WriteString("\n### Examples\n")
	for _, f := range with {
		sb.WriteString("\n`" + f.Path + "`:\n\n```\n")
		for _, e := range f.Examples {
			sb.WriteString(e + "\n")
		}
		sb.WriteString("```\n")
	}
}

func (p *Page) columns(s *Section) []column {
	cols := []column{
		{header: "Name", cell: name},
		{header: "Type", cell: func(f *Field) string { return f.Type }},
		{header: "Required", cell: required},
	}
	cols = appendColumn(cols, s, column{header: "Default", cell: defaultValue})
	cols = appendColumn(cols, s, column{header: "Values", cell: values})
	cols = appendColumn(cols, s, column{header: "Constraints", cell: constraints})
	for _, id := range p.Sources {
		cols = appendColumn(cols, s, column{header: string(id), cell: sourceNames(id)})
	}
	return append(cols, column{header: "Description", cell: description})
}

// appendColumn keeps a column only when some field in the section fills it.
func appendColumn(cols []column, s *Section, c column) []column {
	for _, f := range s.Fields {
		if c.cell(f) != "" {
			return append(cols, c)
		}
	}
	return cols
}

func name(f *Field) string {
	label := "`" + f.Name + "`"
	if f.Section != "" {
		return "[" + label + "](#" + f.Section + ")"
	}
	return label
}

func required(f *Field) string {
	if f.Required {
		return "yes"
	}
	return "no"
}

func defaultValue(f *Field) string {
	if f.Default == "" {
		return ""
	}
	out := "`" + f.Default + "`"
	if !f.DefaultApplied {
		// A documented default does not change what absence resolves to, and
		// a reader who is about to rely on it needs to know that.
		out += " (documented)"
	}
	return out
}

func values(f *Field) string { return code(f.Values, ", ") }

func constraints(f *Field) string { return strings.Join(f.Constraints, "; ") }

func sourceNames(id figureout.SourceID) func(f *Field) string {
	return func(f *Field) string { return code(f.Names[id], ", ") }
}

func description(f *Field) string {
	var parts []string
	if f.Doc != "" {
		parts = append(parts, f.Doc)
	}
	switch {
	case f.MovedTo != "":
		parts = append(parts, "**Deprecated.** Moved to `"+f.MovedTo+"`.")
	case f.Deprecated != "":
		parts = append(parts, "**Deprecated.** "+f.Deprecated)
	}
	if f.Secret {
		parts = append(parts, "**Secret.**")
	}
	if f.Recursive != "" {
		parts = append(parts, "Nests "+f.Recursive+" again.")
	}
	return strings.Join(parts, " ")
}

func code(values []string, sep string) string {
	if len(values) == 0 {
		return ""
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = "`" + v + "`"
	}
	return strings.Join(out, sep)
}

func headers(cols []column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.header
	}
	return out
}

func row(cells []string) string {
	return "| " + strings.Join(cells, " | ") + " |\n"
}

func divider(n int) string {
	cells := make([]string, n)
	for i := range cells {
		cells[i] = "---"
	}
	return row(cells)
}

// escape keeps a cell inside its column: a pipe would end it, and a newline
// would end the row.
func escape(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.Join(strings.Fields(s), " ")
}
