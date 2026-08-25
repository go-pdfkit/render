package render

import (
	"github.com/go-gfx/gfx/raster"
	"testing"

	"github.com/go-opentype/fonts"
	"github.com/go-pdfkit/reader"
)

// pageWithFont builds a page carrying a real embedded TrueType font, so that
// what comes out can be looked at rather than assumed.
func pageWithFont(t *testing.T, content string, extra reader.Dict) *reader.Document {
	t.Helper()
	ttf := fonts.MostLegible()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	file := w.Add(&reader.Stream{
		Dict: reader.Dict{"Length1": reader.Integer(len(ttf))},
		Raw:  ttf,
	})
	descriptor := w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(32), "FontFile2": file,
	})
	widths := make(reader.Array, 0, 224)
	for i := 32; i < 256; i++ {
		widths = append(widths, reader.Integer(600))
	}
	font := reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
		"BaseFont": reader.Name("Test"), "FirstChar": reader.Integer(32),
		"LastChar": reader.Integer(255), "Widths": widths,
		"FontDescriptor": descriptor,
		"Encoding":       reader.Name("WinAnsiEncoding"),
	}
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"Font": reader.Dict{"F": w.Add(font)}},
	}
	for k, v := range extra {
		page[k] = v
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{w.Add(page)}, "Count": reader.Integer(1)})
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

func TestTextIsDrawnWhereItIsPut(t *testing.T) {
	// A capital H at forty points, its baseline at y=10, starting at x=10.
	d := pageWithFont(t, "BT /F 40 Tf 10 10 Td (H) Tj ET", nil)
	img := draw(t, d, Options{})
	if inked(img) == 0 {
		t.Fatal("no text was drawn at all")
	}
	// The glyph stands on its baseline, so there is ink above y=50 in image
	// coordinates and none below it.
	above, below := 0, 0
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			if isWhite(img, x, y) {
				continue
			}
			if y < 50 {
				above++
			} else {
				below++
			}
		}
	}
	if above == 0 {
		t.Error("nothing was drawn above the baseline")
	}
	if below > above/10 {
		t.Errorf("%d pixels fell below the baseline against %d above it", below, above)
	}
	// And nothing to the left of where the text starts.
	for y := 0; y < img.H; y++ {
		for x := 0; x < 8; x++ {
			if !isWhite(img, x, y) {
				t.Fatalf("ink before the text begins, at %s", pixel(img, x, y))
			}
		}
	}
}

func TestTextAdvancesAlongTheLine(t *testing.T) {
	one := draw(t, pageWithFont(t, "BT /F 20 Tf 10 20 Td (H) Tj ET", nil), Options{})
	many := draw(t, pageWithFont(t, "BT /F 20 Tf 10 20 Td (HHHH) Tj ET", nil), Options{})
	if inked(many) <= inked(one)*2 {
		t.Errorf("four letters inked %d pixels against one letter's %d", inked(many), inked(one))
	}
	// The fourth letter is well to the right of the first.
	right := false
	for y := 0; y < many.H; y++ {
		for x := 46; x < many.W; x++ {
			if !isWhite(many, x, y) {
				right = true
			}
		}
	}
	if !right {
		t.Error("the later letters did not move along the line")
	}
}

func TestTextPositioningOperators(t *testing.T) {
	// Td, TD, Tm and T* all have to move the pen; each draws the same letter
	// somewhere different.
	cases := []struct {
		name    string
		content string
	}{
		{"Td", "BT /F 20 Tf 10 30 Td (H) Tj ET"},
		{"TD", "BT /F 20 Tf 10 40 TD (H) Tj ET"},
		{"Tm", "BT /F 20 Tf 1 0 0 1 10 30 Tm (H) Tj ET"},
		{"T*", "BT /F 20 Tf 20 TL 10 50 Td T* (H) Tj ET"},
		{"quote", "BT /F 20 Tf 20 TL 10 50 Td (H) ' ET"},
		{"doublequote", "BT /F 20 Tf 20 TL 10 50 Td 0 0 (H) \" ET"},
	}
	for _, c := range cases {
		img := draw(t, pageWithFont(t, c.content, nil), Options{})
		if inked(img) == 0 {
			t.Errorf("%s: nothing was drawn", c.name)
		}
	}
}

func TestTextRenderingModes(t *testing.T) {
	filled := draw(t, pageWithFont(t, "BT /F 40 Tf 0 Tr 10 10 Td (H) Tj ET", nil), Options{})
	invisible := draw(t, pageWithFont(t, "BT /F 40 Tf 3 Tr 10 10 Td (H) Tj ET", nil), Options{})
	stroked := draw(t, pageWithFont(t, "BT /F 40 Tf 1 Tr 10 10 Td (H) Tj ET", nil), Options{})
	both := draw(t, pageWithFont(t, "BT /F 40 Tf 2 Tr 10 10 Td (H) Tj ET", nil), Options{})
	if inked(invisible) != 0 {
		t.Error("invisible text was drawn")
	}
	if inked(filled) == 0 || inked(stroked) == 0 {
		t.Error("filled or stroked text was not drawn")
	}
	if inked(stroked) >= inked(filled) {
		t.Error("an outline inked as much as a solid letter")
	}
	if inked(both) < inked(filled) {
		t.Error("filling and stroking inked less than filling")
	}
}

func TestTheSizeMatters(t *testing.T) {
	small := draw(t, pageWithFont(t, "BT /F 10 Tf 10 20 Td (H) Tj ET", nil), Options{})
	large := draw(t, pageWithFont(t, "BT /F 40 Tf 10 20 Td (H) Tj ET", nil), Options{})
	if inked(large) <= inked(small)*4 {
		t.Errorf("forty points inked %d against ten points' %d", inked(large), inked(small))
	}
	// A size of nothing draws nothing.
	if inked(draw(t, pageWithFont(t, "BT /F 0 Tf 10 20 Td (H) Tj ET", nil), Options{})) != 0 {
		t.Error("text at no size was drawn")
	}
}

func TestHorizontalScaling(t *testing.T) {
	normal := draw(t, pageWithFont(t, "BT /F 30 Tf 10 20 Td (H) Tj ET", nil), Options{})
	wide := draw(t, pageWithFont(t, "BT /F 30 Tf 200 Tz 10 20 Td (H) Tj ET", nil), Options{})
	if inked(wide) <= inked(normal) {
		t.Errorf("stretched text inked %d against %d", inked(wide), inked(normal))
	}
}

func TestCharacterAndWordSpacing(t *testing.T) {
	// Spacing pushes the later letters along, so the last one lands further
	// right.
	rightmost := func(content string) int {
		img := draw(t, pageWithFont(t, content, nil), Options{})
		for x := img.W - 1; x >= 0; x-- {
			for y := 0; y < img.H; y++ {
				if !isWhite(img, x, y) {
					return x
				}
			}
		}
		return 0
	}
	plain := rightmost("BT /F 12 Tf 5 20 Td (H H) Tj ET")
	spaced := rightmost("BT /F 12 Tf 4 Tc 5 20 Td (H H) Tj ET")
	worded := rightmost("BT /F 12 Tf 10 Tw 5 20 Td (H H) Tj ET")
	if spaced <= plain {
		t.Errorf("character spacing did not push the text along: %d against %d", spaced, plain)
	}
	if worded <= plain {
		t.Errorf("word spacing did not push the text along: %d against %d", worded, plain)
	}
}

func TestAnArrayOfStringsAndNumbers(t *testing.T) {
	// The numbers between the strings move the pen backwards.
	tight := draw(t, pageWithFont(t, "BT /F 20 Tf 10 20 Td [(H) 2000 (H)] TJ ET", nil), Options{})
	loose := draw(t, pageWithFont(t, "BT /F 20 Tf 10 20 Td [(H) -2000 (H)] TJ ET", nil), Options{})

	// A large positive number pulls the second letter back over the first, so
	// less of the page is inked.
	if inked(tight) >= inked(loose) {
		t.Errorf("the numbers did nothing: %d against %d", inked(tight), inked(loose))
	}
}

func TestTextRise(t *testing.T) {
	base := draw(t, pageWithFont(t, "BT /F 20 Tf 10 20 Td (H) Tj ET", nil), Options{})
	raised := draw(t, pageWithFont(t, "BT /F 20 Tf 20 Ts 10 20 Td (H) Tj ET", nil), Options{})
	// Raised text is higher up the page, which is a smaller y in the image.
	first := func(img *raster.Image) int {
		for y := 0; y < img.H; y++ {
			for x := 0; x < img.W; x++ {
				if !isWhite(img, x, y) {
					return y
				}
			}
		}
		return img.H
	}
	if first(raised) >= first(base) {
		t.Errorf("rise did not lift the text: %d against %d", first(raised), first(base))
	}
}

func TestTextThatDrawsNothing(t *testing.T) {
	for _, content := range []string{
		"BT (H) Tj ET",                                      // no font named
		"BT /Missing 20 Tf 10 20 Td (H) Tj ET",              // a font nothing answers to
		"BT /F 20 Tf 10 20 Td 42 Tj ET",                     // an operand that is not a string
		"BT /F 20 Tf 10 20 Td 42 TJ ET",                     // an array that is not one
		"BT /F 20 Tf 10 20 Td [42 /x] TJ ET",                // an array of nothing to draw
		"BT Tf Tc Tw Tz TL Ts Tr Td TD Tm T* Tj TJ ' \" ET", // every operator with nothing
	} {
		img := draw(t, pageWithFont(t, content, nil), Options{})
		if inked(img) != 0 {
			t.Errorf("%q inked %d pixels", content, inked(img))
		}
	}
}
