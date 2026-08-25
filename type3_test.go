package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// pageWithType3 builds a page using a font whose glyphs are content streams:
// the letter A draws a filled square filling its own em, so where it lands and
// how big it is can be read straight off the pixels.
func pageWithType3(t *testing.T, content string, glyph string, extra reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	if glyph == "" {
		glyph = "1000 0 0 0 1000 1000 d1 0 0 1000 1000 re f"
	}
	proc := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(glyph)})
	font := reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type3"),
		"FontBBox":   reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
		"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0), reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
		"CharProcs":  reader.Dict{"square": proc},
		"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
			"Differences": reader.Array{reader.Integer(65), reader.Name("square")}},
		"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
		"Widths": reader.Array{reader.Integer(1000)},
	}
	for k, v := range extra {
		font[k] = v
	}
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"Font": reader.Dict{"F": w.Add(font)}},
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

func TestAType3GlyphIsDrawnAtTheRightSize(t *testing.T) {
	// Twenty points, at (10,10): a square from (10,10) to (30,30) on the page,
	// which is y from 70 to 90 in the image.
	d := pageWithType3(t, "BT /F 20 Tf 10 10 Td (A) Tj ET", "", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 20, 80)
	wantBlack(t, img, 12, 88)
	wantBlack(t, img, 28, 72)
	wantWhite(t, img, 5, 80)
	wantWhite(t, img, 35, 80)
	wantWhite(t, img, 20, 60)
	wantWhite(t, img, 20, 95)
}

func TestAType3GlyphAdvances(t *testing.T) {
	// Two squares side by side, each twenty wide.
	d := pageWithType3(t, "BT /F 20 Tf 10 10 Td (AA) Tj ET", "", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 20, 80)
	wantBlack(t, img, 40, 80)
	wantWhite(t, img, 60, 80)
}

func TestAType3FontMatrixThatIsNotTheUsualOne(t *testing.T) {
	// Half the usual scale: the same glyph comes out half the size.
	d := pageWithType3(t, "BT /F 20 Tf 10 10 Td (A) Tj ET", "",
		reader.Dict{"FontMatrix": reader.Array{reader.Real(0.0005), reader.Integer(0),
			reader.Integer(0), reader.Real(0.0005), reader.Integer(0), reader.Integer(0)}})
	img := draw(t, d, Options{})
	wantBlack(t, img, 15, 85)
	wantWhite(t, img, 25, 75)
}

func TestAType3GlyphDrawsInTheColourInForce(t *testing.T) {
	d := pageWithType3(t, "1 0 0 rg BT /F 20 Tf 10 10 Td (A) Tj ET", "", nil)
	img := draw(t, d, Options{})
	c := img.At(20, 80)
	if c.R < 200 || c.G > 60 {
		t.Errorf("the glyph is %s", pixel(img, 20, 80))
	}
}

func TestType3GlyphsThatDrawNothing(t *testing.T) {
	cases := []struct {
		name  string
		extra reader.Dict
	}{
		{"a code nothing is named for", reader.Dict{"Encoding": reader.Dict{}}},
		{"no procedures at all", reader.Dict{"CharProcs": reader.Integer(1)}},
		{"a procedure that is not a stream", reader.Dict{
			"CharProcs": reader.Dict{"square": reader.Integer(1)}}},
		{"a matrix that is not numbers", reader.Dict{
			"FontMatrix": reader.Array{reader.Name("x"), reader.Integer(0),
				reader.Integer(0), reader.Real(0.001), reader.Integer(0), reader.Integer(0)}}},
	}
	for _, c := range cases {
		d := pageWithType3(t, "BT /F 20 Tf 10 10 Td (A) Tj ET", "", c.extra)
		img := draw(t, d, Options{})
		if c.name == "a matrix that is not numbers" {
			// The usual matrix is used instead, so the glyph is still drawn.
			if inked(img) == 0 {
				t.Errorf("%s: nothing was drawn", c.name)
			}
			continue
		}
		if inked(img) != 0 {
			t.Errorf("%s: %d pixels were inked", c.name, inked(img))
		}
	}
}

func TestAType3GlyphWithItsOwnResources(t *testing.T) {
	// The glyph names a colour space of its own, which only its font's
	// resources can answer for.
	d := pageWithType3(t, "BT /F 20 Tf 10 10 Td (A) Tj ET",
		"1000 0 d0 /S cs 0 sc 0 0 1000 1000 re f",
		reader.Dict{"Resources": reader.Dict{"ColorSpace": reader.Dict{
			"S": reader.Array{reader.Name("CalGray"), reader.Dict{}}}}})
	wantBlack(t, draw(t, d, Options{}), 20, 80)
}

func TestAType3GlyphThatShowsTextItself(t *testing.T) {
	// A glyph whose content stream draws text must not lose the position of
	// the text that showed it.
	d := pageWithType3(t, "BT /F 20 Tf 10 10 Td (AA) Tj ET",
		"1000 0 d0 BT ET 0 0 1000 1000 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 20, 80)
	wantBlack(t, img, 40, 80)
}
