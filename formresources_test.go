package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// pageWhoseFormHasSomeResources builds a page that draws a form whose own
// /Resources is PRESENT and INCOMPLETE: it holds one entry of the category and
// not the one the form's content uses.
//
// That is the shape of qpdf's form-xobjects-some-resources2.pdf, which is in
// the conformance corpus and which this renderer drew two of six images of.
// See go-pdfkit/render#102.
func pageWhoseFormHasSomeResources(t *testing.T, formContent string, formRes, pageRes reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	inner := w.Add(&reader.Stream{
		Dict: reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Form")},
		Raw:  []byte("0 g 0 0 10 10 re f"),
	})
	form := reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Form")}
	if formRes != nil {
		form["Resources"] = formRes
	}
	outer := w.Add(&reader.Stream{Dict: form, Raw: []byte(formContent)})
	res := reader.Dict{"XObject": reader.Dict{"F": outer, "G": inner}}
	for k, v := range pageRes {
		res[k] = v
	}
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(40), reader.Integer(40)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("/F Do")}),
		"Resources": res,
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAFormWithSomeResourcesInheritsTheRest(t *testing.T) {
	// The form lists an /XObject of its own -- so the dictionary is there --
	// and does NOT list /G, which it draws. Before, a present /Resources
	// replaced the page's wholesale and /G resolved to nothing: the renderer
	// returned early and drew paper.
	//
	// The control is the SAME document with the form's own /XObject holding
	// the name. If both drew ink the test would pass with the chain removed.
	some := reader.Dict{"XObject": reader.Dict{"Unused": reader.Name("nothing")}}
	d := pageWhoseFormHasSomeResources(t, "/G Do", some, nil)
	wantBlack(t, draw(t, d, Options{}), 5, 35)

	// And the extraction path, which walks the content stream separately.
	ims, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 0 {
		t.Errorf("a form of forms yielded %d pictures", len(ims))
	}
}

func TestANameNoDictionaryInForceProvidesDrawsNothing(t *testing.T) {
	// The other direction, and the one that keeps the chain from making
	// everything resolve to something: a name nowhere.
	some := reader.Dict{"XObject": reader.Dict{"Unused": reader.Name("nothing")}}
	d := pageWhoseFormHasSomeResources(t, "/Nowhere Do", some, nil)
	wantWhite(t, draw(t, d, Options{}), 5, 35)
	if ims, err := Images(d, 1); err != nil || len(ims) != 0 {
		t.Errorf("got %d pictures, %v", len(ims), err)
	}
}

func TestAGraphicsStateNameNoDictionaryProvidesIsLeftAlone(t *testing.T) {
	// gs names an /ExtGState, and the lookup for it went through the same
	// change. A name nothing provides must leave the state as it was rather
	// than take a nil for a dictionary.
	some := reader.Dict{"XObject": reader.Dict{"Unused": reader.Name("nothing")}}
	d := pageWhoseFormHasSomeResources(t, "/Nowhere gs 0 g 0 0 10 10 re f", some, nil)
	wantBlack(t, draw(t, d, Options{}), 5, 35)
}

func TestAGraphicsStateIsInheritedByAFormThatDoesNotListIt(t *testing.T) {
	// The inheritance has to hold for every category the change touched, and
	// /ExtGState is the one whose effect is visible in one pixel: half alpha
	// over white paper is grey, not black.
	page := reader.Dict{"ExtGState": reader.Dict{"Half": reader.Dict{"ca": reader.Real(0.5)}}}
	some := reader.Dict{"XObject": reader.Dict{"Unused": reader.Name("nothing")}}
	d := pageWhoseFormHasSomeResources(t, "/Half gs 0 g 0 0 10 10 re f", some, page)
	img := draw(t, d, Options{})
	if isBlack(img, 5, 35) || isWhite(img, 5, 35) {
		t.Errorf("the form did not inherit the page's /ExtGState: %s", pixel(img, 5, 35))
	}
}

func TestADictionaryInForceWithoutTheCategoryIsSteppedOver(t *testing.T) {
	// A form whose /Resources holds no /XObject AT ALL is not the absent case
	// -- the dictionary is there, the category is not -- and the walk has to
	// step over it rather than stop.
	some := reader.Dict{"Font": reader.Dict{"Unused": reader.Name("nothing")}}
	d := pageWhoseFormHasSomeResources(t, "/G Do", some, nil)
	wantBlack(t, draw(t, d, Options{}), 5, 35)
}

func TestAResourceThatIsThereAndIsNotWhatItShouldBe(t *testing.T) {
	// Both lookups now answer "not there" with a nil and leave "there but
	// wrong" to the type assertion after it. Before the change a missing name
	// came back as /Null and took that same path, so these two branches were
	// reached by the missing case and never by the wrong-type one.
	some := reader.Dict{"XObject": reader.Dict{"Unused": reader.Name("nothing")}}

	// An /XObject entry that is not a stream.
	d := pageWhoseFormHasSomeResources(t, "/Unused Do", some, nil)
	wantWhite(t, draw(t, d, Options{}), 5, 35)

	// An /ExtGState entry that is not a dictionary, with ink after it so the
	// page says whether the operator took the state down with it.
	page := reader.Dict{"ExtGState": reader.Dict{"Bad": reader.Integer(1)}}
	d = pageWhoseFormHasSomeResources(t, "/Bad gs 0 g 0 0 10 10 re f", some, page)
	wantBlack(t, draw(t, d, Options{}), 5, 35)
}
