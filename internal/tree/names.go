package tree

import (
	"github.com/go-faster/figureout"
)

// Names maps every canonical field path to the document paths one tree source
// accepts for it, primary first.
//
// It walks the model the way [Binder] walks a document, so a renamed member, a
// union whose variants are siblings of its tag and a collection element are
// spelled here exactly as they have to be written.
func Names(m *figureout.Model, source figureout.SourceID) map[string][]string {
	n := &namer{source: source, out: map[string][]string{}}
	n.object(m.Root, "", "")
	return n.out
}

type namer struct {
	source figureout.SourceID
	out    map[string][]string
}

// object walks one object. base is the canonical model path it contributes to,
// prefix the document path leading to it.
func (n *namer) object(obj *figureout.ObjectModel, base, prefix string) {
	for _, f := range obj.Fields {
		names := memberNames(f, n.source)
		if len(names) == 0 {
			continue
		}
		path := base + f.Name
		// Only the field's own segment carries its aliases; an alias deeper in
		// the document is documented where it is declared, rather than
		// multiplying every path below it.
		doc := prefix + names[0]

		// An object, a union and a collection are all written as a member of
		// their own, so each is named here before its contents are.
		n.set(path, prefix, names)

		if f.Moved() {
			// A shadow is the former spelling of another field: it is read
			// under its own name and borrows the target's structure, which is
			// documented there rather than repeated under the old path.
			continue
		}
		if _, ok := f.Recursive(); ok {
			// The object below is one that already encloses this field. Its
			// members are named where they are declared: naming them again
			// under every path that re-enters them would not terminate.
			continue
		}

		switch elem, collection := f.Elements(); {
		case f.Type.Union != nil:
			if p, ok := figureout.DiscriminatorPath(f); ok {
				n.out[p] = []string{doc + "." + f.Type.Union.Discriminator}
			}
			for _, variant := range f.Type.Union.Variants {
				n.object(variant.Object, path+".", doc+".")
			}
		case collection && f.Type.Kind == figureout.TypeList:
			n.object(elem, figureout.ElementPath(path, "")+".", doc+"[].")
		case collection:
			n.object(elem, figureout.ElementPath(path, "")+".", doc+".<key>.")
		case f.Type.Object != nil:
			n.object(f.Type.Object, path+".", doc+".")
		}
	}
}

func (n *namer) set(path, prefix string, names []string) {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, prefix+name)
	}
	n.out[path] = out
}
