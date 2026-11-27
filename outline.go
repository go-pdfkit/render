package render

import (
	"strings"

	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/pdffont"
	"github.com/go-pdfkit/reader"
)

// maxOutlinedGlyphs is how many converted outlines one font keeps. A font
// program has at most 65 536 glyphs, but each outline is as large as the
// program makes it, so what a font retains is bounded here too. A variable so
// that a test can lower it.
var maxOutlinedGlyphs = 4096

// An Outliner reads the fonts of a document the way the renderer does, and
// hands their glyphs out as shapes instead of drawing them. It is how a tool
// that rewrites a content stream turns text into paths without a second
// implementation of which glyph a code stands for: the answer is the one this
// package draws, so a page rewritten with it looks like the page rendered.
//
// An Outliner is not safe for concurrent use.
type Outliner struct{ r *renderer }

// NewOutliner reads the fonts of d.
func NewOutliner(d *reader.Document) *Outliner {
	return &Outliner{r: &renderer{doc: d, fonts: map[int]*pdfFont{}}}
}

// Font reads the font a /Font resource entry stands for: an indirect
// reference or the dictionary itself. The same reference gives the same font,
// read once. ok is false when the entry is not a font dictionary.
func (o *Outliner) Font(entry reader.Object) (*GlyphFont, bool) {
	if ref, isRef := entry.(reader.Ref); isRef {
		if f, hit := o.r.fonts[ref.Num]; hit {
			return &GlyphFont{f, o.r.doc}, true
		}
	}
	dict, ok := reader.ToDict(resolve(o.r.doc, entry))
	if !ok {
		return nil, false
	}
	f := o.r.loadFont(dict)
	if ref, isRef := entry.(reader.Ref); isRef {
		o.r.fonts[ref.Num] = f
	}
	return &GlyphFont{f, o.r.doc}, true
}

// A GlyphFont is one font of a document, ready to be asked for glyphs.
type GlyphFont struct {
	f *pdfFont
	d *reader.Document
}

// GlyphOp is one command of a glyph's outline.
type GlyphOp uint8

// The commands of an outline. A quadratic takes a control point and an end
// point, and a move or a line an end point: P[0] is the control or the end.
const (
	GlyphMove GlyphOp = iota
	GlyphLine
	GlyphQuad
	GlyphClose
)

// A GlyphSeg is one command of an outline and its points, in text space: one
// unit is the font size, so the glyph is placed by scaling it by the size.
type GlyphSeg struct {
	Op GlyphOp
	P  [2][2]float64
}

// Type3 says the glyphs are content streams ([GlyphFont.Type3Proc]) and not
// outlines.
func (g *GlyphFont) Type3() bool { return g.f.Kind() == pdffont.Type3 }

// Simple says codes are single bytes, which is when the word space applies.
func (g *GlyphFont) Simple() bool { return g.f.Kind() != pdffont.Composite }

// Vertical says the font is written top to bottom, which this package does not
// draw: a composite font with a vertical CMap (Identity-V, a name ending in
// -V, or a CMap stream with WMode 1). A caller that cannot reproduce what the
// renderer draws should refuse it.
func (g *GlyphFont) Vertical() bool {
	if g.f.Kind() != pdffont.Composite {
		return false
	}
	switch enc := resolve(g.d, g.f.Dict().Get("Encoding")).(type) {
	case reader.Name:
		return strings.HasSuffix(string(enc), "-V")
	case *reader.Stream:
		w, _ := reader.ToInt(resolve(g.d, enc.Dict.Get("WMode")))
		return w == 1
	}
	return false
}

// Codes splits a string shown with the font into its character codes.
func (g *GlyphFont) Codes(s []byte) []int { return g.f.Codes(s) }

// Advance is how far the pen moves for a code, in text space: a fraction of
// the font size, before the character and word spacing are added.
func (g *GlyphFont) Advance(code int) float64 { return g.f.advance(code) }

// Embolden says the font stands in for a bold face the document does not carry
// and the renderer draws it heavier than its outline.
func (g *GlyphFont) Embolden() bool { return g.f.embolden }

// Outline is the shape of a code in text space, and false when the font has
// none (a code it does not carry, a font program that cannot be read, a
// Type 3 font). A stand-in for an italic is leaned here as it is drawn. The
// slice is shared: it must not be written to.
func (g *GlyphFont) Outline(code int) ([]GlyphSeg, bool) {
	// A Type 3 font has no face, so glyph says no for it.
	segs, ok := g.f.glyph(code)
	if !ok || len(segs) == 0 {
		return nil, false
	}
	// Keyed by the glyph, not the code: a CID font can map tens of thousands of
	// codes onto one glyph, and each would otherwise keep a copy of it.
	gi := g.f.glyphIndex(code)
	if out, hit := g.f.outlined[gi]; hit {
		return out, true
	}
	inv := 1 / g.f.perEm
	lean := 0.0
	if g.f.slant {
		lean = fauxSlant
	}
	out := make([]GlyphSeg, 0, len(segs))
	pt := func(x, y float64) [2]float64 { return [2]float64{(x + lean*y) * inv, y * inv} }
	for _, s := range segs {
		switch s.Op {
		case opentype.SegMoveTo:
			out = append(out, GlyphSeg{Op: GlyphMove, P: [2][2]float64{pt(s.P[0].X, s.P[0].Y)}})
		case opentype.SegLineTo:
			out = append(out, GlyphSeg{Op: GlyphLine, P: [2][2]float64{pt(s.P[0].X, s.P[0].Y)}})
		case opentype.SegQuadTo:
			out = append(out, GlyphSeg{Op: GlyphQuad, P: [2][2]float64{
				pt(s.P[0].X, s.P[0].Y), pt(s.P[1].X, s.P[1].Y)}})
		case opentype.SegClose:
			out = append(out, GlyphSeg{Op: GlyphClose})
		}
	}
	if len(g.f.outlined) < maxOutlinedGlyphs {
		if g.f.outlined == nil {
			g.f.outlined = map[opentype.GlyphIndex][]GlyphSeg{}
		}
		g.f.outlined[gi] = out
	}
	return out, true
}

// Type3Proc is the glyph procedure of a Type 3 font for a code: a content
// stream that draws the glyph in glyph space, which [GlyphFont.FontMatrix]
// maps to text space.
func (g *GlyphFont) Type3Proc(code int) (*reader.Stream, bool) {
	// Only a Type 3 font has /CharProcs, and a code with no name finds nothing
	// in them: both read as no procedure.
	name, _ := g.f.GlyphName(code)
	return reader.ToStream(resolve(g.d, g.f.CharProcs().Get(reader.Name(name))))
}

// FontMatrix is a Type 3 font's glyph space to text space.
func (g *GlyphFont) FontMatrix() [6]float64 { return g.f.FontMatrix() }

// Resources is the /Resources of a Type 3 font, nil when it has none.
func (g *GlyphFont) Resources() reader.Dict { return g.f.Type3Resources() }
