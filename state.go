package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
	"image/color"
)

// A gstate is everything the drawing operators read and write: where things
// go, what colour they are, and what is allowed to be seen.
type gstate struct {
	ctm geometry.Matrix

	fill        color.RGBA
	stroke      color.RGBA
	fillSpace   *space
	strokeSpace *space

	// fillPattern and strokePattern stand in for the colour when the space
	// in force is the Pattern one: what is drawn is then a gradient or a
	// little drawing repeated, rather than one colour.
	fillPattern   *pattern
	strokePattern *pattern

	// fixedColour says the colour cannot be changed from here on, which is
	// what running an uncoloured pattern means: such a pattern is a shape
	// only, drawn in the colour named where it was used, and every colour
	// operator inside it is to be passed over.
	fixedColour bool

	lineWidth  float64
	lineCap    vector.LineCap
	lineJoin   vector.LineJoin
	miterLimit float64
	dash       []float64
	dashPhase  float64

	fillAlpha   float64
	strokeAlpha float64

	// text is everything the show operators read besides the string.
	text textState

	// clip is the coverage every mark is multiplied by, one value per pixel of
	// the image, or nil when nothing is clipped away.
	clip []float32
}

// clone copies a state deeply enough that saving and restoring works: the clip
// is shared until something narrows it, which is what makes q and Q cheap.
func (g gstate) clone() gstate {
	out := g
	out.dash = append([]float64(nil), g.dash...)
	return out
}

// A renderer draws one page.
type renderer struct {
	doc *reader.Document
	img *raster.Image
	rz  vector.Rasterizer

	// depth bounds how far one form may draw another.
	depth int
	// tm is where the next glyph goes and tlm where the current line
	// began; both belong to the page rather than to the graphics state,
	// which is what the specification says.
	tm, tlm geometry.Matrix

	// base is the space a pattern is placed in, however the transform has
	// changed since: the transform the page started in, or — inside a form —
	// the one the form's content started in.
	base geometry.Matrix

	// fonts are the ones already read, by the object they were read from.
	fonts map[int]*pdfFont

	// ops counts what has been drawn, so a file cannot ask for an unbounded
	// amount of work.
	ops int
}

// maxFormDepth is how deeply forms may nest before the page gives up.
const maxFormDepth = 12

// maxOperations bounds how much one page may be asked to draw.
const maxOperations = 1 << 22

// paint puts one coverage grid on the page in one colour, through the clip.
func (r *renderer) paint(g *gstate, cov []float64, ox, oy, w, h int, c color.RGBA, alpha float64) {
	if len(cov) == 0 || alpha <= 0 {
		return
	}
	if alpha < 1 || g.clip != nil {
		cov = r.mask(g, cov, ox, oy, w, h, alpha)
	}
	vector.Composite(r.img, cov, ox, oy, w, h, vector.SolidPaint{Color: c})
}

// mask multiplies a coverage grid by the clip and the alpha, in place on a
// copy, since the rasteriser hands back its own scratch.
func (r *renderer) mask(g *gstate, cov []float64, ox, oy, w, h int, alpha float64) []float64 {
	out := make([]float64, len(cov))
	copy(out, cov)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			v := out[i] * alpha
			if g.clip != nil {
				v *= float64(g.clip[(oy+y)*r.img.W+(ox+x)])
			}
			out[i] = v
		}
	}
	return out
}

// narrow intersects the clip with one coverage grid: what was already hidden
// stays hidden, and everything outside the new shape joins it.
func (r *renderer) narrow(g *gstate, cov []float64, ox, oy, w, h int, ok bool) {
	next := make([]float32, r.img.W*r.img.H)
	if ok {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				next[(oy+y)*r.img.W+(ox+x)] = float32(cov[y*w+x])
			}
		}
	}
	if g.clip != nil {
		for i := range next {
			next[i] *= g.clip[i]
		}
	}
	g.clip = next
}
