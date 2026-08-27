package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
	"image"
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

	// clip is the shape everything is drawn through, or nil when nothing is
	// clipped away.
	clip *clip

	// softMask is a second such grid, and multiplies alongside the clip: a
	// clip is a shape and a soft mask is a picture, but both come down to how
	// much of each pixel a mark is allowed to reach. It is a byte a pixel
	// rather than the clip's float, because a mask is read off an eight-bit
	// picture and holding it wider than it was measured would only cost
	// memory — and a page may name a great many masks.
	softMask []uint8
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

	// softMasks are the ones already drawn, since a file names one graphics
	// state and uses it over and over.
	softMasks map[softMaskKey][]uint8

	// painted records how much of each pixel has been marked, and is set only
	// while a soft mask of the second kind is being drawn — which is the one
	// kind that asks not what came out but whether anything did.
	painted []float32

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
	if alpha < 1 || g.clip != nil || g.softMask != nil {
		cov = r.mask(g, cov, ox, oy, w, h, alpha)
	}
	vector.Composite(r.img, cov, ox, oy, w, h, vector.SolidPaint{Color: c})
	r.record(cov, ox, oy, w, h)
}

// record notes how much of each pixel a mark covered, for a soft mask that
// asks how much was painted rather than how it came out.
func (r *renderer) record(cov []float64, ox, oy, w, h int) {
	if r.painted == nil {
		return
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r.markPixel(ox+x, oy+y, cov[y*w+x])
		}
	}
}

// markPixel adds one mark's coverage to what is already there, the way one
// coat of paint over another leaves less of the paper showing.
func (r *renderer) markPixel(x, y int, a float64) {
	if r.painted == nil || a <= 0 {
		return
	}
	i := y*r.img.W + x
	was := float64(r.painted[i])
	r.painted[i] = float32(was + a*(1-was))
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
				v *= g.clip.at(ox+x, oy+y)
			}
			if g.softMask != nil {
				v *= maskLevel(g.softMask[(oy+y)*r.img.W+(ox+x)])
			}
			out[i] = v
		}
	}
	return out
}

// maskLevel turns one of a mask's bytes into how much it lets through.
func maskLevel(v uint8) float64 { return float64(v) / 255 }

// A clip is what a page is allowed to draw on: the box it has narrowed itself
// down to, and how much of each pixel inside that box is allowed through.
//
// It is kept as a box rather than as a value for every pixel of the page
// because a page narrows its clip over and over — a real government form does
// it two thousand one hundred and forty times, and an arXiv figure six
// thousand seven hundred — and a full page of floats each time is four
// kilobytes per hundred pixels of paper, whether or not the shape covers any
// of it. Measured before this: that form allocated 4 226 MB, and one figure
// asked for 97 333 MB to draw a single page.
type clip struct {
	ox, oy, w, h int
	// cov is one value a pixel over the box, and nothing outside it. A clip
	// whose box is empty lets nothing through at all, which is what a page
	// asks for when it clips to a shape that covers no pixel.
	cov []float32
}

// at is how much of one pixel of the image the clip lets through. Everything
// outside the box is outside the clip.
func (c *clip) at(x, y int) float64 {
	if c == nil {
		return 1
	}
	if x < c.ox || y < c.oy || x >= c.ox+c.w || y >= c.oy+c.h {
		return 0
	}
	return float64(c.cov[(y-c.oy)*c.w+(x-c.ox)])
}

// narrow intersects the clip with one coverage grid: what was already hidden
// stays hidden, and everything outside the new shape joins it.
func (r *renderer) narrow(g *gstate, cov []float64, ox, oy, w, h int, ok bool) {
	if !ok {
		// The shape covered no pixel, so nothing may be drawn from here on.
		g.clip = &clip{}
		return
	}
	// What is left is what both shapes allow, so the new box need be no
	// larger than the smaller of the two — which is what keeps a page that
	// narrows itself repeatedly from paying for the whole sheet each time.
	box := image.Rect(ox, oy, ox+w, oy+h)
	if g.clip != nil {
		box = box.Intersect(image.Rect(g.clip.ox, g.clip.oy,
			g.clip.ox+g.clip.w, g.clip.oy+g.clip.h))
	}
	box = box.Intersect(image.Rect(0, 0, r.img.W, r.img.H))
	if box.Empty() {
		g.clip = &clip{}
		return
	}
	next := &clip{ox: box.Min.X, oy: box.Min.Y, w: box.Dx(), h: box.Dy()}
	next.cov = make([]float32, next.w*next.h)
	for y := 0; y < next.h; y++ {
		for x := 0; x < next.w; x++ {
			px, py := next.ox+x, next.oy+y
			v := float32(cov[(py-oy)*w+(px-ox)])
			if g.clip != nil {
				v *= float32(g.clip.at(px, py))
			}
			next.cov[y*next.w+x] = v
		}
	}
	g.clip = next
}
