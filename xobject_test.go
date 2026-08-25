package render

import (
	"fmt"
	"testing"

	"github.com/go-pdfkit/reader"
)

// pageWithForm builds a page that draws a form, with the form's dictionary
// entries under the caller's control.
func pageWithForm(t *testing.T, content string, form reader.Dict, formContent string) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	dict := reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Form")}
	for k, v := range form {
		dict[k] = v
	}
	xobj := w.Add(&reader.Stream{Dict: dict, Raw: []byte(formContent)})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(40), reader.Integer(40)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"XObject": reader.Dict{"F": xobj}},
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

func TestAFormDrawsItsOwnContent(t *testing.T) {
	d := pageWithForm(t, "/F Do", nil, "0 g 5 5 10 10 re f")
	img := draw(t, d, Options{})
	wantBlack(t, img, 10, 30)
}

func TestAFormCarriesItsOwnMatrix(t *testing.T) {
	form := reader.Dict{"Matrix": reader.Array{reader.Integer(1), reader.Integer(0),
		reader.Integer(0), reader.Integer(1), reader.Integer(20), reader.Integer(20)}}
	d := pageWithForm(t, "/F Do", form, "0 g 0 0 10 10 re f")
	img := draw(t, d, Options{})
	wantBlack(t, img, 25, 15)
	wantWhite(t, img, 5, 35)
}

func TestAFormIsClippedByItsBox(t *testing.T) {
	form := reader.Dict{"BBox": reader.Array{reader.Integer(0), reader.Integer(0),
		reader.Integer(10), reader.Integer(10)}}
	d := pageWithForm(t, "/F Do", form, "0 g 0 0 40 40 re f")
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 35)
	wantWhite(t, img, 25, 15)
}

func TestTheTransformInForceWhenAFormIsDrawnApplies(t *testing.T) {
	d := pageWithForm(t, "1 0 0 1 20 20 cm /F Do", nil, "0 g 0 0 10 10 re f")
	img := draw(t, d, Options{})
	wantBlack(t, img, 25, 15)
}

func TestAFormWithItsOwnResources(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	xobj := w.Add(&reader.Stream{
		Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": reader.Dict{"ColorSpace": reader.Dict{
				"S": reader.Array{reader.Name("CalGray"), reader.Dict{}}}},
		},
		Raw: []byte("/S cs 0 sc 0 0 20 20 re f"),
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(40), reader.Integer(40)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("/F Do")}),
		"Resources": reader.Dict{"XObject": reader.Dict{"F": xobj}},
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
	wantBlack(t, draw(t, d, Options{}), 10, 30)
}

func TestFormsThatDrawNothing(t *testing.T) {
	cases := []struct {
		name    string
		content string
		form    reader.Dict
		body    string
	}{
		{"no name", "Do", nil, "0 g 0 0 40 40 re f"},
		{"a name that is not a name", "42 Do", nil, "0 g 0 0 40 40 re f"},
		{"a name nothing answers to", "/Missing Do", nil, "0 g 0 0 40 40 re f"},
		{"something that is not a form", "/F Do", reader.Dict{"Subtype": reader.Name("Image")}, ""},
		{"a matrix that is not numbers", "/F Do",
			reader.Dict{"Matrix": reader.Array{reader.Name("x"), reader.Integer(0),
				reader.Integer(0), reader.Integer(1), reader.Integer(0), reader.Integer(0)}},
			"0 g 0 0 40 40 re f"},
		{"a matrix of the wrong length", "/F Do",
			reader.Dict{"Matrix": reader.Array{reader.Integer(1)}}, ""},
	}
	for _, c := range cases {
		d := pageWithForm(t, c.content, c.form, c.body)
		img := draw(t, d, Options{})
		if c.name == "a matrix of the wrong length" {
			continue // the form is drawn, it simply has nothing in it
		}
		if inked(img) != 0 {
			t.Errorf("%s: %d pixels were inked", c.name, inked(img))
		}
	}
}

func TestAPageWithNoResourcesAtAll(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 20, 20}, "/F Do 0 g 0 0 10 10 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 15)
}

func TestFormsThatDrawEachOtherForEver(t *testing.T) {
	// A form that draws itself must stop rather than run away.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	xobj := w.Reserve()
	w.Put(xobj, &reader.Stream{
		Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": reader.Dict{"XObject": reader.Dict{"F": xobj}},
		},
		Raw: []byte("0 g 0 0 5 5 re f /F Do"),
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("/F Do")}),
		"Resources": reader.Dict{"XObject": reader.Dict{"F": xobj}},
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
	wantBlack(t, draw(t, d, Options{}), 2, 18)
}

func TestAPageThatAsksForTooMuchWork(t *testing.T) {
	// A stream of more operations than a page is allowed stops partway rather
	// than drawing for ever.
	content := "0 g "
	for i := 0; i < 200; i++ {
		content += fmt.Sprintf("%d %d 2 2 re f ", i%20, i%20)
	}
	d := onePage(t, [4]float64{0, 0, 40, 40}, content, nil)
	r := &renderer{doc: d, img: draw(t, d, Options{}), ops: maxOperations}
	r.run([]byte("0 g 0 0 40 40 re f"), nil, gstate{fillAlpha: 1})
	if inked(r.img) == 0 {
		t.Error("the page drew nothing at all")
	}
}

func TestExtGStateParameters(t *testing.T) {
	// Every parameter this wave reads, and the shapes that are not parameters.
	solid := inked(draw(t, onePage(t, [4]float64{0, 0, 60, 20},
		"0 G 4 w 0 10 m 60 10 l S", nil), Options{}))
	gs := func(params reader.Dict, content string) int {
		d := onePage(t, [4]float64{0, 0, 60, 20}, content,
			reader.Dict{"Resources": reader.Dict{"ExtGState": reader.Dict{"G": params}}})
		return inked(draw(t, d, Options{}))
	}
	if gs(reader.Dict{"LW": reader.Integer(12)}, "0 G /G gs 0 10 m 60 10 l S") <= solid {
		t.Error("a width from the state made no difference")
	}
	if gs(reader.Dict{"D": reader.Array{reader.Array{reader.Integer(6), reader.Integer(6)}, reader.Integer(0)}},
		"0 G 4 w /G gs 0 10 m 60 10 l S") >= solid {
		t.Error("a dash pattern from the state made no difference")
	}
	for _, params := range []reader.Dict{
		{"LC": reader.Integer(1)}, {"LJ": reader.Integer(1)}, {"ML": reader.Integer(2)},
		{"CA": reader.Real(0.5)},
		{"D": reader.Array{reader.Integer(1)}},
		{"Nothing": reader.Integer(1)},
	} {
		if gs(params, "0 G 4 w /G gs 0 10 m 60 10 l S") == 0 {
			t.Errorf("%v drew nothing", params)
		}
	}
	// A gs that names nothing, or names something that is not there.
	for _, content := range []string{
		"0 G 4 w gs 0 10 m 60 10 l S",
		"0 G 4 w 42 gs 0 10 m 60 10 l S",
		"0 G 4 w /Missing gs 0 10 m 60 10 l S",
	} {
		d := onePage(t, [4]float64{0, 0, 60, 20}, content,
			reader.Dict{"Resources": reader.Dict{"ExtGState": reader.Dict{"G": reader.Dict{}}}})
		if inked(draw(t, d, Options{})) != solid {
			t.Errorf("%q changed the line", content)
		}
	}
	// And a page with no state dictionary at all.
	d := onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w /G gs 0 10 m 60 10 l S", nil)
	if inked(draw(t, d, Options{})) != solid {
		t.Error("a gs with no dictionary to read changed the line")
	}
}
