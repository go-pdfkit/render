package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/pdffont"
	"github.com/go-pdfkit/reader"
)

// A pdfFont is a font ready to draw with: what the document says about it,
// which is [pdffont.Font]'s business, and the program its outlines come out
// of, which is this package's.
type pdfFont struct {
	*pdffont.Font

	// program is the embedded font, when there is one, and face is the handle
	// its outlines come through. Outlines arrive in font units.
	program *opentype.Font
	face    *opentype.Face
	perEm   float64

	// fontMatrix is a Type 3 font's glyph space, as a transform.
	fontMatrix geometry.Matrix

	// substituted says the face is a stand-in rather than the font the
	// document named, which is what happens when the document carries no
	// font at all.
	substituted bool
	// embolden and slant say the document named a bold or an italic face and
	// the stand-in is neither, so one is made of the other.
	embolden, slant bool
}

// advance is how far the pen moves for one code, in text space.
//
// It is what the document says, when the document says anything. A font it
// does not carry may say nothing at all — one of the fourteen standard faces
// is written with no widths, since every reader is expected to know Times and
// Helvetica and Courier by heart — and then the stand-in's own advance is the
// answer, which is why the stand-ins are the three faces metric-compatible
// with those.
func (f *pdfFont) advance(code int) float64 {
	if f.HasWidth(code) || !f.substituted || f.face == nil {
		return f.Width(code)
	}
	return float64(f.face.AdvanceIndex(f.glyphIndex(code))) / f.perEm
}

// glyph is the outline of one code, in text space — a thousandth of the point
// size, the units a PDF font is measured in.
func (f *pdfFont) glyph(code int) ([]opentype.Segment, bool) {
	if f.face == nil {
		return nil, false
	}
	return f.face.GlyphOutline(f.glyphIndex(code))
}

// glyphIndex works out which glyph of the font program a code stands for.
func (f *pdfFont) glyphIndex(code int) opentype.GlyphIndex {
	if f.Kind() == pdffont.Composite {
		// How an identifier reaches a glyph depends on which sort of program
		// carries them. A CFF one addressed by identifier maps through its own
		// charset, which lists for each glyph in order the identifier it
		// stands for; the map a document may supply is defined only for the
		// TrueType-based sort and means nothing here.
		//
		// Taking the identifier for the glyph number, which is right for the
		// other sort, is wrong here in the worse of the two ways: past the end
		// of the font it draws nothing, but inside it — which is the usual
		// case — it draws a real glyph and the wrong one. Of the glyphs such
		// fonts were asked for across 5 999 corpus files, 11 148 came out
		// wrong that way against 467 right.
		if f.program != nil && f.program.IsCIDKeyed() {
			if gid, ok := f.program.GlyphIndexByCID(code); ok {
				return gid
			}
			// The font does not name that identifier at all, so there is no
			// glyph to draw and nothing worth guessing from.
			return 0
		}
		gid, _ := f.CIDToGID(code)
		return opentype.GlyphIndex(gid)
	}
	// A symbolic font is addressed through its own character map first;
	// every other kind goes by name, and both fall back on the same last
	// resorts.
	if f.Symbolic() {
		if gid, ok := f.byChar(code); ok {
			return gid
		}
	}
	// A program that names its glyphs — a Type 1 or a CFF one — is addressed
	// by the name the document gives the code, which is the whole point of
	// the encoding a simple font carries.
	if name, ok := f.GlyphName(code); ok {
		if gid, ok := f.program.GlyphIndexByName(name); ok && gid != 0 {
			return gid
		}
		if r, ok := pdffont.RuneOfGlyphName(name); ok {
			if gid, ok := f.program.GlyphIndex(r); ok && gid != 0 {
				return gid
			}
		}
	}
	if gid, ok := f.byChar(code); ok {
		return gid
	}
	// The program's own built-in encoding is what a font with no encoding of
	// its own in the document falls back on, and what a symbolic one meant
	// all along.
	if gid, ok := f.program.GlyphIndexByCode(byte(code)); ok && gid != 0 {
		return gid
	}
	// Nothing said which glyph it is; a font whose codes are its own glyph
	// numbers is the last thing left to try, and a number past the end is
	// refused by the outline lookup rather than here.
	return opentype.GlyphIndex(code)
}

// byChar looks a code up as a character, and then in the range the
// specification reserves for a symbolic font's own glyphs.
func (f *pdfFont) byChar(code int) (opentype.GlyphIndex, bool) {
	for _, r := range []rune{rune(code), rune(0xF000 + code)} {
		if gid, ok := f.program.GlyphIndex(r); ok && gid != 0 {
			return gid, true
		}
	}
	return 0, false
}

// loadFont reads a font dictionary into everything needed to draw with it.
func (r *renderer) loadFont(dict reader.Dict) *pdfFont {
	f := &pdfFont{Font: pdffont.Read(r.doc, dict)}
	m := f.FontMatrix()
	f.fontMatrix = geometry.Matrix{Xx: m[0], Xy: m[1], Yx: m[2], Yy: m[3], X0: m[4], Y0: m[5]}
	r.attachProgram(f)
	if f.program == nil {
		r.attachStandIn(f)
	}
	return f
}

// attachProgram reads whichever font program the descriptor carries, so that
// its outlines can be drawn.
func (r *renderer) attachProgram(f *pdfFont) {
	key, data, ok := f.Program()
	if !ok {
		return
	}
	program, err := readProgram(key, data)
	if err != nil {
		// A program this parser cannot read leaves the font without
		// outlines; its widths still move the pen, so what follows the
		// text stays where it belongs.
		return
	}
	f.program = program
	f.perEm = float64(program.UnitsPerEm())
	f.face = program.NewFace(program.UnitsPerEm())
}

// readProgram decodes an embedded font program. Which of the three keys it
// arrived under says what it is: FontFile2 is TrueType, FontFile is a
// PostScript Type 1 program, and FontFile3 is either a bare CFF program or a
// whole OpenType font — the key alone does not settle that one, so both are
// tried.
func readProgram(key reader.Name, data []byte) (*opentype.Font, error) {
	switch key {
	case "FontFile":
		return opentype.ParseType1(data)
	case "FontFile3":
		if f, err := opentype.Parse(data); err == nil {
			return f, nil
		}
		return opentype.ParseCFF(data)
	}
	return opentype.Parse(data)
}
