package render

import (
	"testing"

	"github.com/go-opentype/fonts"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/reader"
)

// pageWithCIDFont builds a page using a font addressed by character
// identifier, two bytes at a time, which is how a file carrying more than 256
// glyphs is written.
func pageWithCIDFont(t *testing.T, content string, kidExtra, fontExtra reader.Dict) *reader.Document {
	t.Helper()
	ttf := fonts.MostLegible()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	file := w.Add(&reader.Stream{Dict: reader.Dict{"Length1": reader.Integer(len(ttf))}, Raw: ttf})
	descriptor := w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(4), "FontFile2": file,
	})
	kid := reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType2"),
		"BaseFont": reader.Name("Test"), "FontDescriptor": descriptor,
		"DW": reader.Integer(600),
	}
	for k, v := range kidExtra {
		kid[k] = v
	}
	font := reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type0"),
		"BaseFont": reader.Name("Test"), "Encoding": reader.Name("Identity-H"),
		"DescendantFonts": reader.Array{w.Add(kid)},
	}
	for k, v := range fontExtra {
		font[k] = v
	}
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
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

// glyphOf is the identifier the test font gives a letter, so the two-byte
// string a composite font wants can be written.
func glyphOf(t *testing.T, r rune) (hi, lo byte) {
	t.Helper()
	f, err := opentype.Parse(fonts.MostLegible())
	if err != nil {
		t.Fatal(err)
	}
	gid, ok := f.GlyphIndex(r)
	if !ok {
		t.Fatalf("the test font has no %q", r)
	}
	return byte(gid >> 8), byte(gid)
}

func TestACompositeFontIsAddressedTwoBytesAtATime(t *testing.T) {
	hi, lo := glyphOf(t, 'H')
	content := "BT /F 40 Tf 10 10 Td <" + hex(hi) + hex(lo) + "> Tj ET"
	img := draw(t, pageWithCIDFont(t, content, nil, nil), Options{})
	if inked(img) == 0 {
		t.Fatal("nothing was drawn")
	}
	// One glyph, so nothing far to the right.
	for y := 0; y < img.H; y++ {
		for x := 60; x < img.W; x++ {
			if !isWhite(img, x, y) {
				t.Fatalf("a second glyph appeared at %s", pixel(img, x, y))
			}
		}
	}
}

// hex renders one byte as two hexadecimal digits.
func hex(b byte) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[b>>4], digits[b&15]})
}

func TestCompositeWidths(t *testing.T) {
	hi, lo := glyphOf(t, 'H')
	code := "<" + hex(hi) + hex(lo) + hex(hi) + hex(lo) + ">"
	// The default width, then the same pair with a width named one identifier
	// at a time, then a run at a time.
	rightmost := func(kid reader.Dict) int {
		img := draw(t, pageWithCIDFont(t, "BT /F 20 Tf 5 20 Td "+code+" Tj ET", kid, nil), Options{})
		for x := img.W - 1; x >= 0; x-- {
			for y := 0; y < img.H; y++ {
				if !isWhite(img, x, y) {
					return x
				}
			}
		}
		return 0
	}
	gid := reader.Integer(int(hi)<<8 | int(lo))
	narrow := rightmost(reader.Dict{"DW": reader.Integer(200)})
	wide := rightmost(reader.Dict{"DW": reader.Integer(2000)})
	if wide <= narrow {
		t.Errorf("the default width did nothing: %d against %d", wide, narrow)
	}
	listed := rightmost(reader.Dict{"DW": reader.Integer(200),
		"W": reader.Array{gid, reader.Array{reader.Integer(2000)}}})
	if listed <= narrow {
		t.Errorf("a width named one at a time did nothing: %d", listed)
	}
	ranged := rightmost(reader.Dict{"DW": reader.Integer(200),
		"W": reader.Array{gid, gid, reader.Integer(2000)}})
	if ranged <= narrow {
		t.Errorf("a width named for a run did nothing: %d", ranged)
	}
}

func TestCompositeWidthsInShapesNobodyShouldWrite(t *testing.T) {
	hi, lo := glyphOf(t, 'H')
	content := "BT /F 20 Tf 5 20 Td <" + hex(hi) + hex(lo) + "> Tj ET"
	for _, wArray := range []reader.Object{
		reader.Array{reader.Name("x")},
		reader.Array{reader.Integer(1)},
		reader.Array{reader.Integer(1), reader.Integer(2)},
		reader.Array{reader.Integer(2), reader.Integer(1), reader.Integer(500)},
		reader.Array{reader.Integer(1), reader.Array{reader.Name("x")}},
		reader.Integer(7),
	} {
		img := draw(t, pageWithCIDFont(t, content, reader.Dict{"W": wArray}, nil), Options{})
		if inked(img) == 0 {
			t.Errorf("%v: nothing was drawn", wArray)
		}
	}
}

func TestACIDToGIDMap(t *testing.T) {
	hi, lo := glyphOf(t, 'H')
	gid := int(hi)<<8 | int(lo)
	// A map that sends identifier one to the glyph for H.
	table := make([]byte, 4)
	table[2], table[3] = hi, lo
	_ = gid
	d := pageWithCIDFontMap(t, "BT /F 40 Tf 10 10 Td <0001> Tj ET", table)
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("the map did not lead to a glyph")
	}
	// An identifier past the end of the map falls to glyph zero, which is
	// the box a font draws when it has nothing else — not the letter.
	d = pageWithCIDFontMap(t, "BT /F 40 Tf 10 10 Td <0099> Tj ET", table)
	other := draw(t, d, Options{})
	first := draw(t, pageWithCIDFontMap(t, "BT /F 40 Tf 10 10 Td <0001> Tj ET", table), Options{})
	if inked(other) == inked(first) {
		t.Error("an identifier past the end of the map drew the same glyph")
	}
}

// pageWithCIDFontMap is pageWithCIDFont with a map from identifiers to glyphs.
func pageWithCIDFontMap(t *testing.T, content string, table []byte) *reader.Document {
	t.Helper()
	ttf := fonts.MostLegible()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	file := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: ttf})
	descriptor := w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(4), "FontFile2": file,
	})
	kid := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType2"),
		"BaseFont": reader.Name("Test"), "FontDescriptor": descriptor,
		"DW":          reader.Integer(600),
		"CIDToGIDMap": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: table}),
	})
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type0"),
		"BaseFont": reader.Name("Test"), "Encoding": reader.Name("Identity-H"),
		"DescendantFonts": reader.Array{kid},
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"Font": reader.Dict{"F": font}},
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

func TestACompositeFontWithNothingUnderIt(t *testing.T) {
	for _, kids := range []reader.Object{
		reader.Array{}, reader.Integer(7), reader.Array{reader.Integer(7)},
	} {
		d := pageWithCIDFont(t, "BT /F 20 Tf 10 20 Td <0048> Tj ET", nil,
			reader.Dict{"DescendantFonts": kids})
		if inked(draw(t, d, Options{})) != 0 {
			t.Errorf("%v: something was drawn", kids)
		}
	}
}

func TestAnOddNumberOfBytes(t *testing.T) {
	// A composite font reads two bytes at a time; a stray one is left alone.
	d := pageWithCIDFont(t, "BT /F 20 Tf 10 20 Td <48> Tj ET", nil, nil)
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("a single byte drew a glyph")
	}
}
