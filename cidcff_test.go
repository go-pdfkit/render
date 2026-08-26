package render

import (
	"encoding/binary"
	"image/color"
	"testing"

	"github.com/go-pdfkit/reader"
)

// A composite font's identifiers reach its glyphs differently depending on
// which sort of program carries them, and the difference is invisible until a
// font is built whose charset is not the identity. So one is built here.
//
// The font has two glyphs: the empty one every font begins with, and a filled
// square. Its charset says that square is identifier 100 — so a reader that
// takes the identifier for the glyph number looks for glyph 100, finds the
// font has two, and draws nothing.

// cffIndexOf lays out a CFF INDEX: how many items, how wide an offset is, the
// offsets, then the items.
func cffIndexOf(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte{0, 0}
	}
	out := []byte{byte(len(items) >> 8), byte(len(items)), 4}
	at := uint32(1)
	for _, it := range items {
		out = binary.BigEndian.AppendUint32(out, at)
		at += uint32(len(it))
	}
	out = binary.BigEndian.AppendUint32(out, at)
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// cffInt writes a dictionary operand at a fixed five bytes, so that the layout
// does not change when the numbers in it do.
func cffInt(v int) []byte {
	return append([]byte{29}, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// cidKeyedCFF builds a font addressed by identifier whose one drawable glyph
// answers to the identifier given, and to no other.
func cidKeyedCFF(cid int) []byte {
	// A square five hundred units on a side, drawn from the origin: move,
	// three sides, and the fill closes the fourth. Nought is one byte, the
	// value plus 139; five hundred and minus five hundred each take two.
	square := []byte{
		139, 139, 21, // 0 0 rmoveto
		248, 136, 139, 5, // 500 0 rlineto
		139, 248, 136, 5, // 0 500 rlineto
		252, 136, 139, 5, // -500 0 rlineto
		14, // endchar
	}
	charStrings := cffIndexOf([][]byte{{14}, square})
	// The charset, format 0: one identifier for each glyph after the first.
	charset := append([]byte{0}, byte(cid>>8), byte(cid))

	name := cffIndexOf([][]byte{[]byte("T")})
	strings := cffIndexOf([][]byte{[]byte("Adobe"), []byte("Identity")})
	gsubrs := cffIndexOf(nil)

	// The top dictionary, laid out twice: once to learn how long it is, and
	// once with the offsets that length decides.
	build := func(charsetAt, charStringsAt int) []byte {
		var d []byte
		d = append(d, cffInt(391)...)       // registry: the first of our own strings
		d = append(d, cffInt(392)...)       // ordering: the second
		d = append(d, cffInt(0)...)         // supplement
		d = append(d, 12, 30)               // ROS: addressed by identifier
		d = append(d, cffInt(charsetAt)...) //
		d = append(d, 15)                   // charset
		d = append(d, cffInt(charStringsAt)...)
		d = append(d, 17) // CharStrings
		return d
	}
	header := []byte{1, 0, 4, 1}
	topLen := len(cffIndexOf([][]byte{build(0, 0)}))
	before := len(header) + len(name) + topLen + len(strings) + len(gsubrs)
	charsetAt := before
	charStringsAt := before + len(charset)
	top := cffIndexOf([][]byte{build(charsetAt, charStringsAt)})

	out := append([]byte{}, header...)
	out = append(out, name...)
	out = append(out, top...)
	out = append(out, strings...)
	out = append(out, gsubrs...)
	out = append(out, charset...)
	out = append(out, charStrings...)
	return out
}

// pageWithCIDKeyedCFF puts one glyph of such a font on a page, addressed by
// the identifier given.
func pageWithCIDKeyedCFF(t *testing.T, program []byte, cid int) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	file := w.Add(&reader.Stream{Dict: reader.Dict{
		"Subtype": reader.Name("CIDFontType0C")}, Raw: program})
	descriptor := w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(4), "FontFile3": file,
	})
	kid := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType0"),
		"BaseFont": reader.Name("Test"), "FontDescriptor": descriptor,
		"DW": reader.Integer(600),
		"CIDSystemInfo": reader.Dict{"Registry": reader.String("Adobe"),
			"Ordering": reader.String("Identity"), "Supplement": reader.Integer(0)},
	})
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type0"),
		"BaseFont": reader.Name("Test"), "Encoding": reader.Name("Identity-H"),
		"DescendantFonts": reader.Array{kid},
	})
	content := []byte{}
	content = append(content, []byte("BT /F 40 Tf 10 10 Td <")...)
	content = append(content, []byte(hex4(cid))...)
	content = append(content, []byte("> Tj ET")...)
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(60), reader.Integer(60)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: content}),
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

// hex4 writes an identifier the way a two-byte string holds one.
func hex4(v int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		digits[(v>>12)&15], digits[(v>>8)&15], digits[(v>>4)&15], digits[v&15],
	})
}

func TestAnIdentifierReachesItsGlyphThroughTheFontsOwnCharset(t *testing.T) {
	// The square answers to identifier 100 and is glyph 1. A reader that took
	// the identifier for the glyph number would look for glyph 100, find the
	// font has two, and draw nothing at all.
	d := pageWithCIDKeyedCFF(t, cidKeyedCFF(100), 100)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 20, 40, color.RGBA{A: 255}, 40)
}

func TestAnIdentifierTheFontDoesNotNameDrawsNothing(t *testing.T) {
	// Not the glyph that happens to have that number: the font does not say
	// what identifier 7 is, so there is nothing to draw and nothing worth
	// guessing from.
	d := pageWithCIDKeyedCFF(t, cidKeyedCFF(100), 7)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 60; y += 6 {
		for x := 0; x < 60; x += 6 {
			if !isWhite(img, x, y) {
				t.Fatalf("something was drawn at (%d,%d) for an identifier the font does not name", x, y)
			}
		}
	}
}
