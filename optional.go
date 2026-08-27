package render

import "github.com/go-pdfkit/reader"

// Optional content is how a document says that some of what it contains is a
// layer, and which layers are to be shown when it is opened. A page's content
// is not a promise that all of it is to be drawn.
//
// # WHAT THE CORPUS SAYS, WHICH IS LESS THAN IT FIRST APPEARED
//
// Optional content is common: 275 of the 1 633 real forms — the eleven issuing
// bodies, not the vendor test suites — carry an /OCProperties, as do 151 of
// 6 667 arXiv files. 161 of those forms hide at least one group, and every one
// of the 123 that names a /BaseState names /OFF, which says that nothing is
// shown unless it is listed as on.
//
// And yet reading all of that changes nothing anyone can see. Walking every
// page of all 8 300 files and counting the operators that put a mark on the
// page, not one of them falls inside a layer that the document's own default
// configuration hides. The layers are declared and left empty — what an
// authoring tool leaves behind. Rendering the 1 633 forms before and after
// this change gives 8 300 identical pages and the same total ink to the pixel.
//
// So this is not a fix for something the corpus shows going wrong. It is here
// because a document's statement about what it shows should be obeyed, and
// because the corpus is forms and figures: layers are how a CAD drawing, a map,
// a multilingual overlay and a print-only mark are written, and none of those
// are in it. The measurement says this corpus does not exercise the mechanism,
// which is a different claim from the mechanism not mattering.
//
// What is read here is the default configuration, /OCProperties/D, which is
// what a viewer shows when it opens a file with nobody there to tick boxes.
// The alternative configurations in /Configs are for a viewer with a layer
// panel and are not read.
type optional struct {
	// off names the groups that are not to be shown. A group absent from it
	// is shown, so a document with no optional content leaves it empty and
	// nothing is hidden.
	off map[reader.Ref]bool
}

// readOptional works out which groups the default configuration turns off.
//
// The order matters and the specification leaves one case open: a group named
// in both /ON and /OFF. /ON is applied first here, so /OFF wins, which is the
// safer of the two readings — showing content a document tried to hide is the
// worse mistake.
func readOptional(d *reader.Document) optional {
	o := optional{off: map[reader.Ref]bool{}}
	// The error is dropped deliberately: reader.Open refuses a file whose
	// trailer does not lead to a catalogue, so every document that exists has
	// one. A branch that cannot be reached is a branch that cannot be tested,
	// and an untested branch in the code that decides what a reader is shown
	// is worse than no branch at all. A nil dictionary answers "no" to the
	// only question asked of it here.
	cat, _ := d.Catalog()
	props, ok := reader.ToDict(resolve(d, cat.Get("OCProperties")))
	if !ok {
		return o
	}
	config, ok := reader.ToDict(resolve(d, props.Get("D")))
	if !ok {
		return o
	}
	// /BaseState /OFF turns every group the document declares off, and then
	// /ON names the ones that come back. The default is /ON, which turns
	// nothing off.
	if state, _ := reader.ToName(resolve(d, config.Get("BaseState"))); state == "OFF" {
		for _, g := range refsOf(d, props.Get("OCGs")) {
			o.off[g] = true
		}
	}
	for _, g := range refsOf(d, config.Get("ON")) {
		delete(o.off, g)
	}
	for _, g := range refsOf(d, config.Get("OFF")) {
		o.off[g] = true
	}
	return o
}

// refsOf reads an array of references, skipping whatever is not one. A group is
// identified by the object it is, not by its contents: two layers may have the
// same name and the same everything else.
func refsOf(d *reader.Document, o reader.Object) []reader.Ref {
	arr, ok := reader.ToArray(resolve(d, o))
	if !ok {
		return nil
	}
	out := make([]reader.Ref, 0, len(arr))
	for _, e := range arr {
		if ref, ok := e.(reader.Ref); ok {
			out = append(out, ref)
		}
	}
	return out
}

// hidden says whether an /OC entry — on an XObject, on an annotation, or named
// by a BDC operator — points at something that is not to be shown.
//
// The entry is either a group or a membership dictionary. A membership
// dictionary names several groups and a policy for combining them.
func (o optional) hidden(d *reader.Document, entry reader.Object) bool {
	ref, isRef := entry.(reader.Ref)
	if isRef && o.off[ref] {
		return true
	}
	dict, ok := reader.ToDict(resolve(d, entry))
	if !ok {
		return false
	}
	if kind, _ := reader.ToName(resolve(d, dict.Get("Type"))); kind != "OCMD" {
		// A group. Whether it is off has already been settled above; a group
		// written directly rather than as a reference cannot be named in /ON
		// or /OFF, so it is shown.
		return false
	}
	// A visibility expression, /VE, is a nested and/or/not over the groups. It
	// takes precedence over /P where a document has both. Not one of the 1 633
	// real forms carries one, so reading it would be guesswork tested against
	// nothing; a document that has one is treated as visible, which is what
	// this did before optional content was read at all.
	groups := membership(d, dict.Get("OCGs"))
	if len(groups) == 0 {
		return false
	}
	on, offCount := 0, 0
	for _, g := range groups {
		if o.off[g] {
			offCount++
			continue
		}
		on++
	}
	// The policy says what makes the content visible; this returns the
	// opposite. /AnyOn is the default.
	switch policy, _ := reader.ToName(resolve(d, dict.Get("P"))); policy {
	case "AllOn":
		return offCount > 0
	case "AnyOff":
		return offCount == 0
	case "AllOff":
		return on > 0
	default: // AnyOn
		return on == 0
	}
}

// membership reads an /OCGs entry, which is either one group or an array of
// them. A single group written without an array is the commoner form.
func membership(d *reader.Document, o reader.Object) []reader.Ref {
	if ref, ok := o.(reader.Ref); ok {
		if _, isArray := reader.ToArray(resolve(d, o)); !isArray {
			return []reader.Ref{ref}
		}
	}
	return refsOf(d, o)
}

// hiddenProperty answers a BDC operator: BDC /OC /name puts the marks that
// follow on a layer, where the name is looked up in the page's
// /Resources/Properties. The dictionary may also be written out in place of
// the name.
func (r *renderer) hiddenProperty(operands []reader.Object, resources reader.Dict) bool {
	if len(operands) < 2 {
		return false
	}
	if tag, _ := reader.ToName(operands[0]); tag != "OC" {
		return false
	}
	if name, ok := reader.ToName(operands[1]); ok {
		props, ok := r.doc.GetDict(resources, "Properties")
		if !ok {
			return false
		}
		return r.oc.hidden(r.doc, props.Get(name))
	}
	return r.oc.hidden(r.doc, operands[1])
}

// suppressed says whether what is being drawn is inside a layer that is off.
// It suppresses marks and nothing else: a clip narrowed inside a hidden layer
// still narrows, because clipping is not marking, and the operators that move
// the pen still move it.
func (r *renderer) suppressed() bool { return r.hideAt != 0 }
