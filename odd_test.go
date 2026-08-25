package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

func TestAStrokingPattern(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 20, 20},
		"/Pattern CS /P SCN 4 w 0 0 m 20 20 l S", reader.Dict{"Resources": reader.Dict{}})
	img := draw(t, d, Options{})
	if inked(img) == 0 {
		t.Error("a stroking pattern drew nothing at all")
	}
}

func TestTheEvenOddClip(t *testing.T) {
	// A square with a square inside it, clipped even-odd: the middle is not
	// clipped in, so a fill over the whole page leaves a hole.
	d := onePage(t, [4]float64{0, 0, 40, 40},
		"0 0 40 40 re 10 10 20 20 re W* n 0 g 0 0 40 40 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 20)
	wantWhite(t, img, 20, 20)
}

func TestAPageSmallerThanAPixel(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 0.4, 0.4}, "0 g 0 0 1 1 re f", nil)
	img := draw(t, d, Options{})
	if img.W != 1 || img.H != 1 {
		t.Errorf("image is %dx%d, want one pixel", img.W, img.H)
	}
}

func TestAPageWhoseContentIsNotContent(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents": w.Add(&reader.Stream{
			Dict: reader.Dict{"Filter": reader.Name("DCTDecode")}, Raw: []byte("not a jpeg")}),
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
	if _, err := Page(d, 1, Options{}); err == nil {
		t.Error("want an error")
	}
}

func TestBoxesInShapesNobodyShouldWrite(t *testing.T) {
	cases := []struct {
		name string
		box  reader.Object
		w, h int
	}{
		{"an element that is not a number", reader.Array{reader.Name("x"),
			reader.Integer(0), reader.Integer(10), reader.Integer(10)}, 612, 792},
		{"no width", reader.Array{reader.Integer(5), reader.Integer(0),
			reader.Integer(5), reader.Integer(10)}, 612, 792},
		{"no height", reader.Array{reader.Integer(0), reader.Integer(5),
			reader.Integer(10), reader.Integer(5)}, 612, 792},
		{"too few numbers", reader.Array{reader.Integer(0), reader.Integer(0)}, 612, 792},
		{"not an array", reader.Integer(7), 612, 792},
		{"the corners the other way round", reader.Array{reader.Integer(30), reader.Integer(40),
			reader.Integer(10), reader.Integer(10)}, 20, 30},
	}
	for _, c := range cases {
		w := reader.NewWriter("1.7")
		pagesRef := w.Reserve()
		page := w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef, "MediaBox": c.box,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")}),
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
		img := draw(t, d, Options{})
		if img.W != c.w || img.H != c.h {
			t.Errorf("%s: image is %dx%d, want %dx%d", c.name, img.W, img.H, c.w, c.h)
		}
	}
}

func TestColourSpacesThatPointAtEachOther(t *testing.T) {
	// A chain of names deeper than the walk allows ends in grey rather than
	// running away.
	spaces := reader.Dict{}
	for i := 0; i < 20; i++ {
		spaces[reader.Name(letter(i))] = reader.Name(letter(i + 1))
	}
	d := onePage(t, [4]float64{0, 0, 20, 20}, "/"+letter(0)+" cs 0 sc 0 0 20 20 re f",
		reader.Dict{"Resources": reader.Dict{"ColorSpace": spaces}})
	if img := draw(t, d, Options{}); img == nil {
		t.Error("nothing came back")
	}
}

// letter names the i'th colour space of the chain above.
func letter(i int) string { return "Cs" + string(rune(97+i%26)) }

func TestThePatternSpaceByArray(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 20, 20}, "/S cs /P scn 0 0 20 20 re f",
		reader.Dict{"Resources": reader.Dict{"ColorSpace": reader.Dict{
			"S": reader.Array{reader.Name("Pattern")}}}})
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("a pattern space named by array drew nothing")
	}
}

func TestTheShortNamesForTheDeviceSpaces(t *testing.T) {
	for _, name := range []string{"G", "RGB", "CMYK", "CalGray", "CalRGB", "DeviceGray"} {
		d := onePage(t, [4]float64{0, 0, 20, 20}, "/"+name+" cs 0 0 20 20 re f", nil)
		if inked(draw(t, d, Options{})) == 0 {
			t.Errorf("/%s drew nothing", name)
		}
	}
}

func TestSeveralInksWithNoNames(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 20, 20}, "/S cs 1 sc 0 0 20 20 re f",
		reader.Dict{"Resources": reader.Dict{"ColorSpace": reader.Dict{
			"S": reader.Array{reader.Name("DeviceN"), reader.Array{},
				reader.Name("DeviceGray"), reader.Dict{}}}}})
	wantBlack(t, draw(t, d, Options{}), 10, 10)
}

func TestNothingAtAllIsDrawnAtNoOpacity(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 20, 20}, "/G gs 0 g 0 0 20 20 re f",
		reader.Dict{"Resources": reader.Dict{
			"ExtGState": reader.Dict{"G": reader.Dict{"ca": reader.Real(0)}}}})
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn at no opacity at all")
	}
}

func TestAFormWhoseContentIsNotContent(t *testing.T) {
	d := pageWithForm(t, "/F Do",
		reader.Dict{"Filter": reader.Name("DCTDecode")}, "not a jpeg")
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("a form that cannot be read drew something")
	}
}
