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

// The three stand-ins, each read once and shared.
var (
	sansStandIn  = &substitute{ttf: arimo.TTF}
	serifStandIn = &substitute{ttf: tinos.TTF}
	monoStandIn  = &substitute{ttf: cousine.TTF}
)

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

// standIn picks the face to draw a font that carries no program of its own.
func (r *renderer) standIn(f *pdffont.Font) *substitute {
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
	program, err := r.standIn(f.Font).get()
	if err != nil {
		return
	}
	f.program = program
	f.perEm = float64(program.UnitsPerEm())
	f.face = program.NewFace(program.UnitsPerEm())
	f.substituted = true
	f.embolden, f.slant = r.wantsBoldItalic(f.Font)
}

// wantsBoldItalic reads whether the font the document named was a bold or an
// italic one. Only one weight of each stand-in is carried, so a bold is drawn
// by stroking the outline as well as filling it and an italic by leaning it
// over — which is what a typesetter calls a faux bold and a faux italic, and
// what every reader does with a face it has not got in the weight asked for.
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

// faux is how far a faux bold is stroked, as a fraction of the em. It is what
// a typesetter would reach for: enough to read as bold beside the same face
// unemboldened, not so much that the counters fill in.
const fauxBoldWidth = 0.024

// fauxSlant is how far a faux italic leans, as a tangent: about twelve
// degrees, which is where most italics sit.
const fauxSlant = 0.21
