package figureout

import (
	"fmt"
	"strings"
)

// Redacted replaces a secret value wherever the library would otherwise format
// one.
const Redacted = "[redacted]"

// Secret marks a field as carrying a credential.
//
// [Hidden] is documentation metadata: it keeps a field out of generated docs
// and does nothing else, so a Pattern or MinLength failure is one constraint
// away from printing a token into a log. Secret has teeth. A secret field's
// value never appears in a message the library formats — not in a constraint
// failure, not in a decoding error from a source — and [Report.Secret] lets a
// consumer honor the same rule in its own logging.
//
//	figureout.Explicit(s, &c.Token, "token", figureout.Secret()).NonEmpty()
//
// Secret redacts values, not names. A credential still appears in generated
// documentation, because its name is what an operator needs in order to supply
// it, while its default and examples render as [Redacted]. Pair it with
// [Hidden] to leave a field out of the reference entirely. Generated JSON
// Schema marks the property "writeOnly".
func Secret() FieldOption {
	return FieldOptionFunc(func(c FieldOptionContext) error {
		return c.AddMetadata(Metadata{Secret: true})
	})
}

// Redact removes every rendering of values from msg when f is a secret field.
//
// Sources call it before reporting a decoding failure, so that "invalid integer
// \"hunter2\"" never reaches a log. Over-redaction is the safe direction: a
// message may lose more than the value itself, and that is preferred to leaking
// it.
func Redact(f *FieldModel, msg string, values ...any) string {
	if f == nil || !f.Meta.Secret {
		return msg
	}
	for _, v := range values {
		s := fmt.Sprint(v)
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, s, Redacted)
	}
	return msg
}

// Secret reports whether the value at the canonical path is a credential.
//
// A consumer walking [Report.Origins] to log where its configuration came from
// can use it to keep the values it prints alongside them out of the log.
func (r *Report) Secret(path string) bool {
	_, ok := r.secrets[path]
	return ok
}

// Secrets iterates over every resolved path holding a credential.
func (r *Report) Secrets() func(func(string) bool) {
	return func(yield func(string) bool) {
		for path := range r.secrets {
			if !yield(path) {
				return
			}
		}
	}
}
