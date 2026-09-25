package render

import (
	"strings"
	"sync"

	"github.com/go-opentype/fonts/arimo"
	"github.com/go-opentype/fonts/cousine"
	"github.com/go-opentype/fonts/tinos"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/pdffont"
	"github.com/go-pdfkit/reader"
)

// A PDF need not carry the font its text is set in. Fourteen faces — four
// weights each of Helvetica, Times and Courier, plus Symbol and
// ZapfDingbats — every reader is expected to have of its own, and in practice
// a file names any font at all and hopes.
//
// Of 95 818 corpus pages that show text, 40 437 name a font the file does not
// carry and 38 663 carry none at all. Drawing nothing for those is not an
// option: it is two pages in five with no text on them.
//
// So a stand-in is drawn. Arimo, Tinos and Cousine are metric-compatible with
// Helvetica, Times and Courier — the same advances, glyph for glyph — so text
// set in one of the standard faces comes out the width it was meant to be even
// where the document gives no widths of its own. For any other name the
// descriptor's own flags say whether it wanted a serif, a sans or a typewriter,
// and one of the three is drawn in its place.
//
// What this cannot do is invent a font's shapes. A page set in a face nobody
// has is a page set in something else, and it says so nowhere: that is what
// every reader does, and it is better than a blank page by the width of the
// text on it.
type substitute struct {
	once sync.Once
	ttf  []byte
	font *opentype.Font
	err  error
}

// A family is the four faces of one stand-in. A face is nil when the family
// bundles no file for it, and [family.pick] says what to do about that in one
// place rather than at each call site.
type family struct {
	regular, bold, italic, boldItalic *substitute
}

// The three stand-ins, each face read once and shared.
var (
	sansStandIn = &family{
		regular:    &substitute{ttf: arimo.TTF},
		bold:       &substitute{ttf: arimo.Bold},
		italic:     &substitute{ttf: arimo.Italic},
		boldItalic: &substitute{ttf: arimo.BoldItalic},
	}
	serifStandIn = &family{
		regular:    &substitute{ttf: tinos.TTF},
		bold:       &substitute{ttf: tinos.Bold},
		italic:     &substitute{ttf: tinos.Italic},
		boldItalic: &substitute{ttf: tinos.BoldItalic},
	}
	monoStandIn = &family{
		regular:    &substitute{ttf: cousine.TTF},
		bold:       &substitute{ttf: cousine.Bold},
		italic:     &substitute{ttf: cousine.Italic},
		boldItalic: &substitute{ttf: cousine.BoldItalic},
	}
)

// pick returns the face to draw for the weight and slope the document asked
// for, and whether a faux bold or a faux slant is still needed on top of it
// because this family bundles no file for that combination.
//
// A real face is preferred over a faked one in every case, INCLUDING the one
// where only half of what was asked for exists: a bold italic drawn from the
// bold face and leaned over is closer than the regular face stroked AND
// leaned. Each fake is decided separately, because they are separate
// distortions -- stroking changes ink, leaning changes shape, and neither
// changes the advance the document is laid out with.
func (f *family) pick(bold, italic bool) (s *substitute, embolden, slant bool) {
	switch {
	case bold && italic:
		switch {
		case f.boldItalic != nil:
			return f.boldItalic, false, false
		case f.bold != nil:
			return f.bold, false, true
		case f.italic != nil:
			return f.italic, true, false
		}
	case bold:
		if f.bold != nil {
			return f.bold, false, false
		}
	case italic:
		if f.italic != nil {
			return f.italic, false, false
		}
	}
	return f.regular, bold, italic
}

// get parses the stand-in, once.
func (s *substitute) get() (*opentype.Font, error) {
	s.once.Do(func() { s.font, s.err = opentype.Parse(s.ttf) })
	return s.font, s.err
}

// The flags a font descriptor carries that say what a face looks like.
const (
	flagFixedPitch = 1 << 0
	flagSerif      = 1 << 1
	flagItalic     = 1 << 6
)

// standIn picks the family to draw a font that carries no program of its own.
func (r *renderer) standIn(f *pdffont.Font) *family {
	name := strings.ToLower(baseFontName(r.doc, f))
	switch {
	case strings.Contains(name, "courier") || strings.Contains(name, "mono"):
		return monoStandIn
	case strings.Contains(name, "times") || strings.Contains(name, "roman") ||
		strings.Contains(name, "serif") || strings.Contains(name, "georgia") ||
		strings.Contains(name, "garamond") || strings.Contains(name, "book"):
		// "DejaVu Sans" holds "sans" and would be caught below; the serif
		// names are checked first because "DejaVu Serif" holds neither
		// "times" nor "roman".
		return serifStandIn
	case strings.Contains(name, "helvetica") || strings.Contains(name, "arial") ||
		strings.Contains(name, "sans"):
		return sansStandIn
	}
	// Nothing in the name said: the descriptor's flags are what is left.
	flags, _ := reader.ToInt(resolve(r.doc, f.Descriptor().Get("Flags")))
	switch {
	case flags&flagFixedPitch != 0:
		return monoStandIn
	case flags&flagSerif != 0:
		return serifStandIn
	}
	return sansStandIn
}

// baseFontName is what the document calls the font, without the six-letter tag
// a subsetted one carries in front of it.
func baseFontName(d *reader.Document, f *pdffont.Font) string {
	name, _ := reader.ToName(resolve(d, f.Dict().Get("BaseFont")))
	s := string(name)
	if len(s) > 7 && s[6] == '+' {
		return s[7:]
	}
	return s
}

// attachStandIn gives a font with no program of its own a face to be drawn
// with, and leaves it alone when there is nothing sensible to draw: a
// composite font is addressed by glyph number, and a stand-in's glyph numbers
// are its own, so drawing one would put arbitrary letters on the page.
//
// Symbol and ZapfDingbats are left alone for the same reason: no face here
// carries their glyphs, and something is not better than nothing when the
// something is the wrong alphabet.
func (r *renderer) attachStandIn(f *pdfFont) {
	if f.Kind() == pdffont.Composite || f.Kind() == pdffont.Type3 {
		return
	}
	switch strings.ToLower(baseFontName(r.doc, f.Font)) {
	case "symbol", "zapfdingbats", "dingbats":
		return
	}
	bold, italic := r.wantsBoldItalic(f.Font)
	face, embolden, slant := r.standIn(f.Font).pick(bold, italic)
	program, err := face.get()
	if err != nil {
		return
	}
	f.program = program
	f.perEm = float64(program.UnitsPerEm())
	f.face = program.NewFace(program.UnitsPerEm())
	f.substituted = true
	f.embolden, f.slant = embolden, slant
}

// wantsBoldItalic reads whether the font the document named was a bold or an
// italic one. What is then DRAWN is [family.pick]'s business: where the family
// bundles that face it is used, and only where it does not is the weight faked
// by stroking the outline or the slope by leaning it over.
//
// The difference is not cosmetic. A faked bold leaves the ADVANCES of the
// regular face, and a standard font named with no /Widths of its own is laid
// out from them: Helvetica-Bold sets `m` at 889/1000 em where Helvetica sets
// it at 833.
func (r *renderer) wantsBoldItalic(f *pdffont.Font) (embolden, slant bool) {
	name := strings.ToLower(baseFontName(r.doc, f))
	embolden = strings.Contains(name, "bold") || strings.Contains(name, "black") ||
		strings.Contains(name, "heavy") || strings.Contains(name, "semibold")
	slant = strings.Contains(name, "italic") || strings.Contains(name, "oblique")
	if !slant {
		flags, _ := reader.ToInt(resolve(r.doc, f.Descriptor().Get("Flags")))
		slant = flags&flagItalic != 0
	}
	if !embolden {
		// A stem width past about a hundred and sixty thousandths is a bold
		// face; the descriptor is the only place a document says so in a
		// number rather than in a name.
		if w, ok := reader.ToFloat(resolve(r.doc, f.Descriptor().Get("StemV"))); ok && w >= 120 {
			embolden = true
		}
	}
	return embolden, slant
}

// fauxBoldWidth is how far a faux bold is stroked, as a fraction of the em. It
// is what a typesetter would reach for: enough to read as bold beside the same
// face unemboldened, not so much that the counters fill in.
//
// The three metric-compatible families all bundle a real bold, so this is now
// reached only by a family that does not -- it is a fallback, not the road taken.
const fauxBoldWidth = 0.024

// fauxSlant is how far a faux italic leans, as a tangent: about twelve
// degrees, which is where most italics sit.
const fauxSlant = 0.21
