package figureout

import (
	"fmt"
	"slices"
	"strings"
)

// Severity classifies a [Diagnostic].
type Severity uint8

// Severity levels.
const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
)

// String implements [fmt.Stringer].
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	default:
		return "error"
	}
}

// Diagnostic codes reported by descriptor compilation and schema generation.
const (
	CodeMissingDefinition   = "field.missing_definition"
	CodeDuplicateField      = "field.duplicate_registration"
	CodeForeignPointer      = "field.foreign_pointer"
	CodeAmbiguousZeroSize   = "field.ambiguous_zero_size"
	CodeUnsupportedType     = "field.unsupported_type"
	CodeDuplicateName       = "name.duplicate"
	CodeSourceNameCollision = "source.name_collision"
	CodeSourceUnsupported   = "source.unsupported"
	CodeConstraintMismatch  = "constraint.type_mismatch"
	CodeDefaultMismatch     = "default.type_mismatch"
	CodeUnionInvalid        = "union.invalid"
	CodeDeprecated          = "field.deprecated"
	CodeMovedConflict       = "field.moved_conflict"
	CodeInvariantViolated   = "invariant.violated"
	CodeValidatorNotExport  = "validator.not_exportable"
)

// Diagnostic is a structured problem report.
type Diagnostic struct {
	Severity Severity
	Code     string
	Message  string

	// FieldPath is the canonical configuration path, such as "server.port".
	FieldPath string
	// GoPath is the Go path, such as "Config.Server.Port".
	GoPath string
	Source SourceID
	Target TargetID
	Origin *Origin
}

// Error implements [error].
func (d Diagnostic) Error() string {
	var sb strings.Builder
	sb.WriteString(d.Code)
	if d.FieldPath != "" {
		fmt.Fprintf(&sb, " [%s]", d.FieldPath)
	} else if d.GoPath != "" {
		fmt.Fprintf(&sb, " [%s]", d.GoPath)
	}
	sb.WriteString(": ")
	sb.WriteString(d.Message)
	if d.Origin != nil {
		fmt.Fprintf(&sb, " (%s)", d.Origin)
	}
	return sb.String()
}

// Diagnostics is a collection of [Diagnostic].
type Diagnostics []Diagnostic

// HasErrors reports whether any diagnostic has [SeverityError].
func (ds Diagnostics) HasErrors() bool {
	return slices.ContainsFunc(ds, func(d Diagnostic) bool {
		return d.Severity == SeverityError
	})
}

// Err returns ds if it contains errors, and nil otherwise.
func (ds Diagnostics) Err() error {
	if !ds.HasErrors() {
		return nil
	}
	return ds
}

// Error implements [error]. It formats every diagnostic, not just the first.
func (ds Diagnostics) Error() string {
	switch len(ds) {
	case 0:
		return "no diagnostics"
	case 1:
		return ds[0].Error()
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d diagnostics:", len(ds))
	for _, d := range ds {
		sb.WriteString("\n  ")
		sb.WriteString(d.Error())
	}
	return sb.String()
}

func (ds *Diagnostics) errorf(code, goPath, fieldPath, format string, args ...any) {
	*ds = append(*ds, Diagnostic{
		Severity:  SeverityError,
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		GoPath:    goPath,
		FieldPath: fieldPath,
	})
}
