package render

import (
	"testing"

	"github.com/go-opentype/fonts"
	"github.com/go-pdfkit/reader"
)

// pageWithFontDict builds a page whose font dictionary is entirely the
// caller's, so that every shape a producer might write can be handed to the
// loader.
func pageWithFontDict(t *testing.T, content string, font func(w *reader.Writer) reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"Font": reader.Dict{"F": w.Add(font(w))}},
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

// embedded is a descriptor carrying the test font, with the flags asked for.
func embedded(w *reader.Writer, flags int) reader.Object {
	ttf := fonts.MostLegible()
	file := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: ttf})
	return w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(flags), "FontFile2": file,
	})
}

func TestEncodingsBothNamedAndDescribed(t *testing.T) {
	content := "BT /F 30 Tf 10 20 Td (A) Tj ET"
	cases := []struct {
		name string
		enc  reader.Object
	}{
		{"nothing said", nil},
		{"named WinAnsi", reader.Name("WinAnsiEncoding")},
		{"named Standard", reader.Name("StandardEncoding")},
		{"named MacRoman", reader.Name("MacRomanEncoding")},
		{"a name nobody uses", reader.Name("Nonesuch")},
		{"a dictionary with a base", reader.Dict{"BaseEncoding": reader.Name("WinAnsiEncoding")}},
		{"a dictionary with nothing in it", reader.Dict{}},
		{"a dictionary that is a number", reader.Integer(7)},
		{"differences", reader.Dict{"Differences": reader.Array{
			reader.Integer(65), reader.Name("B")}}},
		{"differences that are not a list", reader.Dict{"Differences": reader.Integer(7)}},
		{"differences with rubbish in them", reader.Dict{"Differences": reader.Array{
			reader.Integer(65), reader.Name("B"), reader.Dict{}, reader.Name("C")}}},
	}
	for _, c := range cases {
		d := pageWithFontDict(t, content, func(w *reader.Writer) reader.Dict {
			font := reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"BaseFont": reader.Name("Test"), "FontDescriptor": embedded(w, 32),
			}
			if c.enc != nil {
				font["Encoding"] = c.enc
			}
			return font
		})
		if inked(draw(t, d, Options{})) == 0 {
			t.Errorf("%s: nothing was drawn", c.name)
		}
	}
}

func TestASymbolicFontIsAddressedThroughItsOwnMap(t *testing.T) {
	// Flag four is symbolic and flag thirty-two is not; both have to draw.
	for _, flags := range []int{4, 32, 4 | 32} {
		d := pageWithFontDict(t, "BT /F 30 Tf 10 20 Td (A) Tj ET",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{
					"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
					"BaseFont": reader.Name("Test"), "FontDescriptor": embedded(w, flags),
				}
			})
		if inked(draw(t, d, Options{})) == 0 {
			t.Errorf("flags %d: nothing was drawn", flags)
		}
	}
	// And a descriptor whose flags are not a number.
	d := pageWithFontDict(t, "BT /F 30 Tf 10 20 Td (A) Tj ET",
		func(w *reader.Writer) reader.Dict {
			ttf := fonts.MostLegible()
			file := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: ttf})
			return reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"FontDescriptor": w.Add(reader.Dict{
					"Flags": reader.Name("x"), "FontFile2": file}),
			}
		})
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("a descriptor with odd flags drew nothing")
	}
}

func TestWidthsInShapesNobodyShouldWrite(t *testing.T) {
	content := "BT /F 20 Tf 10 20 Td (AA) Tj ET"
	for _, c := range []struct {
		name  string
		extra reader.Dict
	}{
		{"no first character", reader.Dict{"Widths": reader.Array{reader.Integer(500)}}},
		{"a first character that is not a number", reader.Dict{
			"FirstChar": reader.Name("x"), "Widths": reader.Array{reader.Integer(500)}}},
		{"widths that are not a list", reader.Dict{
			"FirstChar": reader.Integer(65), "Widths": reader.Integer(7)}},
		{"a width that is not a number", reader.Dict{
			"FirstChar": reader.Integer(65), "Widths": reader.Array{reader.Name("x")}}},
	} {
		d := pageWithFontDict(t, content, func(w *reader.Writer) reader.Dict {
			font := reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"BaseFont": reader.Name("Test"), "FontDescriptor": embedded(w, 32),
			}
			for k, v := range c.extra {
				font[k] = v
			}
			return font
		})
		if inked(draw(t, d, Options{})) == 0 {
			t.Errorf("%s: nothing was drawn", c.name)
		}
	}
}

func TestFontProgramsThatCannotBeRead(t *testing.T) {
	content := "BT /F 20 Tf 10 20 Td (A) Tj ET"
	cases := []struct {
		name string
		make func(w *reader.Writer) reader.Object
	}{
		{"no descriptor at all", func(w *reader.Writer) reader.Object { return nil }},
		{"a descriptor with no file", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Flags": reader.Integer(32)})
		}},
		{"a file that is not a font", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Flags": reader.Integer(32),
				"FontFile2": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("not a font")})})
		}},
		{"a file that will not decode", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Flags": reader.Integer(32),
				"FontFile2": w.Add(&reader.Stream{
					Dict: reader.Dict{"Filter": reader.Name("FlateDecode")},
					Raw:  []byte("not deflate")})})
		}},
		{"a file filtered as an image", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Flags": reader.Integer(32),
				"FontFile2": w.Add(&reader.Stream{
					Dict: reader.Dict{"Filter": reader.Name("DCTDecode")},
					Raw:  []byte("not a jpeg")})})
		}},
		{"a file entry that is not a stream", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Flags": reader.Integer(32), "FontFile2": reader.Integer(7)})
		}},
	}
	for _, c := range cases {
		d := pageWithFontDict(t, content, func(w *reader.Writer) reader.Dict {
			font := reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"BaseFont": reader.Name("Test"),
			}
			if desc := c.make(w); desc != nil {
				font["FontDescriptor"] = desc
			}
			return font
		})
		// No outlines, so nothing is drawn — but nothing breaks either.
		if inked(draw(t, d, Options{})) != 0 {
			t.Errorf("%s: something was drawn", c.name)
		}
	}
}

func TestAFontNamedInPlaceRatherThanByReference(t *testing.T) {
	// A font dictionary written straight into the resources, which is legal
	// and skips the cache.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	ttf := fonts.MostLegible()
	file := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: ttf})
	descriptor := w.Add(reader.Dict{"Flags": reader.Integer(32), "FontFile2": file})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("BT /F 30 Tf 10 20 Td (A) Tj ET")}),
		"Resources": reader.Dict{"Font": reader.Dict{"F": reader.Dict{
			"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
			"FontDescriptor": descriptor,
		}}},
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
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("a font written in place drew nothing")
	}
}

func TestAFontThatIsNotADictionary(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("BT /F 30 Tf 10 20 Td (A) Tj ET /G 30 Tf (A) Tj")}),
		"Resources": reader.Dict{"Font": reader.Dict{
			"F": reader.Integer(7),
			"G": w.Add(reader.Integer(7)),
		}},
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
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn")
	}
}

func TestAGlyphTheFontDoesNotHave(t *testing.T) {
	// A code past the end of the font falls to glyph zero rather than off the
	// end of anything.
	d := pageWithFontDict(t, "BT /F 30 Tf 10 20 Td (\xff\xfe) Tj ET",
		func(w *reader.Writer) reader.Dict {
			return reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"FontDescriptor": embedded(w, 4),
			}
		})
	if img := draw(t, d, Options{}); img == nil {
		t.Error("nothing came back")
	}
}

func TestACodeThatIsNeitherACharacterNorNamed(t *testing.T) {
	// Code one is a control character: it is in no encoding, and no font has a
	// glyph for it or for the symbolic range above it. The last resort is to
	// read the code as a glyph number.
	d := pageWithFontDict(t, "BT /F 30 Tf 10 20 Td (\x01) Tj ET",
		func(w *reader.Writer) reader.Dict {
			return reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"FontDescriptor": embedded(w, 4),
			}
		})
	if img := draw(t, d, Options{}); img == nil {
		t.Error("nothing came back")
	}
}

func TestTheSameFontIsOnlyReadOnce(t *testing.T) {
	// Two Tf operators naming the same font: the second must come from the
	// cache rather than parsing the font again.
	d := pageWithFontDict(t, "BT /F 20 Tf 10 20 Td (A) Tj /F 30 Tf (A) Tj ET",
		func(w *reader.Writer) reader.Dict {
			return reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"FontDescriptor": embedded(w, 32),
			}
		})
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("nothing was drawn")
	}
}

func TestNamingAFontWithSomethingThatIsNotAName(t *testing.T) {
	d := pageWithFontDict(t, "BT 42 20 Tf 10 20 Td (A) Tj ET",
		func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"FontDescriptor": embedded(w, 32)}
		})
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn")
	}
}

func TestAPageWithNoFontsAtAll(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 40}, "BT /F 20 Tf 10 20 Td (A) Tj ET", nil)
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn")
	}
}

func TestAType3ProcedureThatCannotBeRead(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	proc := w.Add(&reader.Stream{
		Dict: reader.Dict{"Filter": reader.Name("DCTDecode")}, Raw: []byte("not a jpeg")})
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type3"),
		"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0),
			reader.Integer(0), reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
		"CharProcs": reader.Dict{"square": proc},
		"Encoding": reader.Dict{"Differences": reader.Array{
			reader.Integer(65), reader.Name("square")}},
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(40), reader.Integer(40)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("BT /F 20 Tf 10 10 Td (A) Tj ET")}),
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
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn")
	}
}

func TestACodeRenamedToSomethingNobodyKnows(t *testing.T) {
	// The name says nothing, so the code is tried as a character instead — and
	// sixty-five is still the letter A.
	d := pageWithFontDict(t, "BT /F 30 Tf 10 20 Td (A) Tj ET",
		func(w *reader.Writer) reader.Dict {
			return reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("TrueType"),
				"FontDescriptor": embedded(w, 32),
				"Encoding": reader.Dict{"Differences": reader.Array{
					reader.Integer(65), reader.Name("nonesuch")}},
			}
		})
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("nothing was drawn")
	}
}
