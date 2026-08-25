package render

import (
	"testing"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// pageNamingAFont builds a page whose font the file does not carry, which is
// how two pages of the corpus in five are written.
func pageNamingAFont(t *testing.T, content string, font reader.Dict) *reader.Document {
	t.Helper()
	return pageWithFontDict(t, content, func(w *reader.Writer) reader.Dict {
		out := reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("Type1")}
		for k, v := range font {
			out[k] = v
		}
		if desc, ok := out["FontDescriptor"]; ok {
			if d, ok := desc.(reader.Dict); ok {
				out["FontDescriptor"] = w.Add(d)
			}
		}
		return out
	})
}

func TestAFontTheFileDoesNotCarryIsStillDrawn(t *testing.T) {
	// Of 95 818 corpus pages that show text, 40 437 name a font the file does
	// not carry and 38 663 carry none at all. Drawing nothing for those would
	// be two pages in five with no text on them.
	d := pageNamingAFont(t, "BT /F 20 Tf 5 20 Td (Hello) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica")})
	img := draw(t, d, Options{})
	if inked(img) == 0 {
		t.Fatal("a page naming Helvetica drew nothing")
	}
}

func TestTheStandInAFontNameAsksFor(t *testing.T) {
	cases := []struct {
		name string
		base string
		want *substitute
	}{
		{"Helvetica", "Helvetica", sansStandIn},
		{"a subsetted Helvetica", "ABCDEF+Helvetica-Bold", sansStandIn},
		{"Arial", "ArialMT", sansStandIn},
		{"anything sans", "DejaVuSans-Oblique", sansStandIn},
		{"Times", "Times-Roman", serifStandIn},
		{"Times New Roman", "TimesNewRomanPSMT", serifStandIn},
		{"anything serif", "DejaVuSerif", serifStandIn},
		{"Georgia", "Georgia", serifStandIn},
		{"Garamond", "AGaramondPro", serifStandIn},
		{"a book face", "Bookman", serifStandIn},
		{"Courier", "Courier-Bold", monoStandIn},
		{"anything mono", "RobotoMono", monoStandIn},
		{"a name that says nothing", "Wingbat", sansStandIn},
		{"no name at all", "", sansStandIn},
	}
	for _, c := range cases {
		r, f := fontOf(t, reader.Dict{"BaseFont": reader.Name(c.base)})
		if got := r.standIn(f.Font); got != c.want {
			t.Errorf("%s: the wrong stand-in", c.name)
		}
	}
	// With nothing in the name, the descriptor's flags decide.
	for _, c := range []struct {
		name  string
		flags int64
		want  *substitute
	}{
		{"fixed pitch", flagFixedPitch, monoStandIn},
		{"serif", flagSerif, serifStandIn},
		{"neither", 0, sansStandIn},
	} {
		r, f := fontOf(t, reader.Dict{"BaseFont": reader.Name("Wingbat"),
			"FontDescriptor": reader.Dict{"Flags": reader.Integer(c.flags)}})
		if got := r.standIn(f.Font); got != c.want {
			t.Errorf("%s: the wrong stand-in", c.name)
		}
	}
}

func TestWhatIsNotGivenAStandIn(t *testing.T) {
	// A composite font is addressed by glyph number and a stand-in's numbers
	// are its own, so drawing one would put arbitrary letters on the page.
	// Symbol and the dingbats are left alone for the same reason: no face
	// here carries their glyphs.
	for _, c := range []struct {
		name string
		font reader.Dict
	}{
		{"Symbol", reader.Dict{"BaseFont": reader.Name("Symbol")}},
		{"ZapfDingbats", reader.Dict{"BaseFont": reader.Name("ZapfDingbats")}},
		{"a subsetted Symbol", reader.Dict{"BaseFont": reader.Name("ABCDEF+Symbol")}},
	} {
		d := pageNamingAFont(t, "BT /F 20 Tf 5 20 Td (Hello) Tj ET", c.font)
		if inked(draw(t, d, Options{})) != 0 {
			t.Errorf("%s: something was drawn", c.name)
		}
	}
	// A composite font, likewise.
	d := pageWithFontDict(t, "BT /F 20 Tf 5 20 Td (\x00A) Tj ET", func(w *reader.Writer) reader.Dict {
		kid := w.Add(reader.Dict{"Subtype": reader.Name("CIDFontType2")})
		return reader.Dict{"Subtype": reader.Name("Type0"),
			"Encoding": reader.Name("Identity-H"), "DescendantFonts": reader.Array{kid}}
	})
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("a composite font with no program drew something")
	}
	// And a Type 3 font, whose glyphs are its own drawings.
	d = pageWithFontDict(t, "BT /F 20 Tf 5 20 Td (a) Tj ET", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Subtype": reader.Name("Type3"),
			"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0),
				reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
			"CharProcs": reader.Dict{}}
	})
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("a Type 3 font with no drawings drew something")
	}
}

func TestABoldOrItalicFaceMadeOfARegularOne(t *testing.T) {
	// Only one weight of each stand-in is carried, so a bold is drawn by
	// stroking the outline as well as filling it and an italic by leaning it.
	plain := inked(draw(t, pageNamingAFont(t, "BT /F 30 Tf 5 20 Td (HH) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica")}), Options{}))
	bold := inked(draw(t, pageNamingAFont(t, "BT /F 30 Tf 5 20 Td (HH) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica-Bold")}), Options{}))
	if bold <= plain {
		t.Errorf("bold drew %d pixels and regular %d", bold, plain)
	}
	// An italic covers different pixels without covering many more.
	italic := draw(t, pageNamingAFont(t, "BT /F 30 Tf 5 20 Td (HH) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica-Oblique")}), Options{})
	upright := draw(t, pageNamingAFont(t, "BT /F 30 Tf 5 20 Td (HH) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica")}), Options{})
	same := true
	for y := 0; y < italic.H && same; y++ {
		for x := 0; x < italic.W; x++ {
			if italic.At(x, y) != upright.At(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("an italic came out upright")
	}
}

func TestEveryWayADocumentSaysBoldOrItalic(t *testing.T) {
	cases := []struct {
		name         string
		font         reader.Dict
		bold, italic bool
	}{
		{"nothing at all", reader.Dict{"BaseFont": reader.Name("Helvetica")}, false, false},
		{"bold in the name", reader.Dict{"BaseFont": reader.Name("Helvetica-Bold")}, true, false},
		{"black in the name", reader.Dict{"BaseFont": reader.Name("Roboto-Black")}, true, false},
		{"heavy in the name", reader.Dict{"BaseFont": reader.Name("Something-Heavy")}, true, false},
		{"semibold in the name", reader.Dict{"BaseFont": reader.Name("Open-Semibold")}, true, false},
		{"italic in the name", reader.Dict{"BaseFont": reader.Name("Times-Italic")}, false, true},
		{"oblique in the name", reader.Dict{"BaseFont": reader.Name("Helvetica-Oblique")}, false, true},
		{"both in the name", reader.Dict{"BaseFont": reader.Name("Times-BoldItalic")}, true, true},
		{"italic in the flags", reader.Dict{"BaseFont": reader.Name("Plain"),
			"FontDescriptor": reader.Dict{"Flags": reader.Integer(flagItalic)}}, false, true},
		{"a stem width that is bold", reader.Dict{"BaseFont": reader.Name("Plain"),
			"FontDescriptor": reader.Dict{"StemV": reader.Integer(160)}}, true, false},
		{"a stem width that is not", reader.Dict{"BaseFont": reader.Name("Plain"),
			"FontDescriptor": reader.Dict{"StemV": reader.Integer(80)}}, false, false},
		{"a stem width that is not a number", reader.Dict{"BaseFont": reader.Name("Plain"),
			"FontDescriptor": reader.Dict{"StemV": reader.Name("thick")}}, false, false},
	}
	for _, c := range cases {
		r, f := fontOf(t, c.font)
		bold, italic := r.wantsBoldItalic(f.Font)
		if bold != c.bold || italic != c.italic {
			t.Errorf("%s: bold=%v italic=%v, want %v and %v", c.name, bold, italic, c.bold, c.italic)
		}
	}
}

func TestAStandInGivesTheWidthsTheDocumentDoesNot(t *testing.T) {
	// One of the fourteen standard faces is written with no widths at all.
	// Half an em for every letter reads as a typewriter, which is not what
	// the page says — so the stand-in's own advances are used, and they are
	// the standard face's because the stand-ins are metric-compatible with
	// them.
	d := pageNamingAFont(t, "BT /F 20 Tf 2 20 Td (iiiii) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Times-Roman")})
	narrow := rightmostInk(draw(t, d, Options{}))
	d = pageNamingAFont(t, "BT /F 20 Tf 2 20 Td (mmmmm) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Times-Roman")})
	wide := rightmostInk(draw(t, d, Options{}))
	if wide <= narrow {
		t.Errorf("five m's reach %d and five i's reach %d", wide, narrow)
	}
	// A document that does say how wide its letters are is believed.
	widths := make(reader.Array, 0, 224)
	for i := 32; i < 256; i++ {
		widths = append(widths, reader.Integer(1000))
	}
	d = pageNamingAFont(t, "BT /F 20 Tf 2 20 Td (iiiii) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Times-Roman"),
			"FirstChar": reader.Integer(32), "LastChar": reader.Integer(255),
			"Widths": widths})
	if told := rightmostInk(draw(t, d, Options{})); told <= narrow {
		t.Errorf("widths of a whole em reached %d, against %d without them", told, narrow)
	}
}

// rightmostInk is the last column of the image that has anything on it.
func rightmostInk(img *raster.Image) int {
	for x := img.W - 1; x >= 0; x-- {
		for y := 0; y < img.H; y++ {
			if !isWhite(img, x, y) {
				return x
			}
		}
	}
	return -1
}

// fontOf builds a renderer over a document holding one font dictionary and
// hands back both, for a test about how a font is read rather than drawn.
func fontOf(t *testing.T, font reader.Dict) (*renderer, *pdfFont) {
	t.Helper()
	d := pageNamingAFont(t, "BT /F 12 Tf 5 20 Td (a) Tj ET", font)
	p, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := d.GetDict(p, "Resources")
	fonts, _ := d.GetDict(res, "Font")
	dict, ok := d.GetDict(fonts, "F")
	if !ok {
		t.Fatal("the font is not there")
	}
	r := &renderer{doc: d, fonts: map[int]*pdfFont{}}
	return r, r.loadFont(dict)
}

func TestWhenAStandInCannotBeRead(t *testing.T) {
	// The bundled faces parse — that is what their own package's tests are
	// for — but a font that cannot be read leaves the document's font with no
	// outlines rather than half a face.
	was := sansStandIn
	t.Cleanup(func() { sansStandIn = was })
	sansStandIn = &substitute{ttf: []byte("not a font at all")}

	d := pageNamingAFont(t, "BT /F 20 Tf 5 20 Td (Hello) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica")})
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn from a stand-in that is not a font")
	}
}

func TestTextSetAtANegativeSize(t *testing.T) {
	// A size below nothing turns the text over rather than being refused, and
	// a bold one has no outline to thicken because the width would be below
	// nothing too.
	d := pageNamingAFont(t, "BT /F -20 Tf 50 40 Td (Hello) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica-Bold")})
	if _, err := Page(d, 1, Options{}); err != nil {
		t.Fatal(err)
	}
}
