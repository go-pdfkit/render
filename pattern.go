package render

import (
	"image/color"
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-pdfkit/reader"
)

// A pattern is what a page uses instead of a colour when what it wants is not
// one: a gradient, or a little drawing repeated over an area. Both are named
// through the Pattern colour space and then used as a fill or a stroke.
type pattern struct {
	// shading is set for a shading pattern, the second kind.
	shading *shading
	// content, resources, bbox, xstep and ystep are what a tiling pattern —
	// the first kind, and by far the commoner — is made of.
	content   []byte
	resources reader.Dict
	bbox      []float64
	xstep     float64
	ystep     float64
	// matrix maps the pattern's own space onto the page's, which is the
	// default space of the page rather than whatever transform is in force.
	matrix geometry.Matrix
	// uncoloured says the pattern draws in the colour the page names rather
	// than in colours of its own.
	uncoloured bool
	colour     color.RGBA
}

// readPattern reads a pattern out of the page's resources.
func (r *renderer) readPattern(name reader.Name, resources reader.Dict) *pattern {
	pats, ok := r.doc.GetDict(resources, "Pattern")
	if !ok {
		return nil
	}
	entry := resolve(r.doc, pats.Get(name))
	dict, ok := reader.ToDict(entry)
	stream, isStream := reader.ToStream(entry)
	if isStream {
		dict, ok = stream.Dict, true
	}
	if !ok {
		return nil
	}
	p := &pattern{matrix: geometry.Identity()}
	if m, ok := matrixOf(r.floatArray(dict.Get("Matrix"))); ok {
		p.matrix = m
	}
	kind, _ := reader.ToInt(resolve(r.doc, dict.Get("PatternType")))
	switch kind {
	case 2:
		p.shading = r.readShading(dict.Get("Shading"), resources)
		if p.shading == nil {
			return nil
		}
		return p
	case 1:
		if !isStream {
			return nil
		}
		data, img, err := r.salvaged(stream)
		if err != nil || img != "" {
			return nil
		}
		p.content = data
		p.resources, _ = r.doc.GetDict(dict, "Resources")
		if p.resources == nil {
			p.resources = resources
		}
		p.bbox = r.floatArray(dict.Get("BBox"))
		if len(p.bbox) < 4 {
			return nil
		}
		p.xstep, _ = reader.ToFloat(resolve(r.doc, dict.Get("XStep")))
		p.ystep, _ = reader.ToFloat(resolve(r.doc, dict.Get("YStep")))
		if p.xstep == 0 {
			p.xstep = p.bbox[2] - p.bbox[0]
		}
		if p.ystep == 0 {
			p.ystep = p.bbox[3] - p.bbox[1]
		}
		if paint, _ := reader.ToInt(resolve(r.doc, dict.Get("PaintType"))); paint == 2 {
			p.uncoloured = true
		}
		return p
	}
	return nil
}

// paintShading puts a shading on the page wherever a coverage grid says to.
// The grid is the shape being filled, or the whole clip when the sh operator
// asked for the shading on its own.
func (r *renderer) paintShading(g *gstate, sh *shading, m geometry.Matrix, cov []float64, ox, oy, w, h int, alpha float64) {
	inv, ok := m.Invert()
	if !ok || alpha <= 0 {
		return
	}
	var drawn *meshRaster
	if sh.mesh != nil {
		drawn = sh.mesh.rasterise(m, ox, oy, w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := cov[y*w+x] * alpha
			if g.clip != nil {
				a *= g.clip.at(ox+x, oy+y)
			}
			if g.softMask != nil {
				a *= maskLevel(g.softMask[(oy+y)*r.img.W+(ox+x)])
			}
			if a <= 0 {
				continue
			}
			p := inv.TransformPoint(geometry.Point{X: float64(ox+x) + 0.5, Y: float64(oy+y) + 0.5})
			var c color.RGBA
			var ok bool
			if drawn != nil {
				if c, ok = drawn.at(ox+x, oy+y); !ok || !sh.insideBBox(p.X, p.Y) {
					c, ok = sh.away()
				}
			} else {
				c, ok = sh.at(p.X, p.Y)
			}
			if !ok {
				continue
			}
			r.img.Set(ox+x, oy+y, blend(r.img.At(ox+x, oy+y), c, a))
			r.markPixel(ox+x, oy+y, a)
		}
	}
}

// wholeClip is a coverage grid of one everywhere the clip allows, which is
// what the sh operator paints over.
func (r *renderer) wholeClip(g *gstate) ([]float64, int, int, int, int) {
	cov := make([]float64, r.img.W*r.img.H)
	for i := range cov {
		cov[i] = 1
	}
	return cov, 0, 0, r.img.W, r.img.H
}

// drawShading is the sh operator: paint a shading over everything the clip
// allows, in the space the current transform names.
func (r *renderer) drawShading(g *gstate, operands []reader.Object, resources reader.Dict) {
	if len(operands) == 0 {
		return
	}
	name, ok := reader.ToName(operands[0])
	if !ok {
		return
	}
	shadings, ok := r.doc.GetDict(resources, "Shading")
	if !ok {
		return
	}
	sh := r.readShading(shadings.Get(name), resources)
	if sh == nil {
		return
	}
	cov, ox, oy, w, h := r.wholeClip(g)
	r.paintShading(g, sh, g.ctm, cov, ox, oy, w, h, g.fillAlpha)
}

// paintCoverage puts a rasterised shape on the page in the colour in force --
// or, when that colour is a PATTERN, by running the pattern's own content
// through the shape.
//
// It is one function because it is one rule: a pattern IS a colour, so every
// place that paints with a colour has to honour it. Written out at each call
// site instead, it was honoured when filling a path and forgotten when filling
// a glyph, so a tiling pattern set as the text colour drew solid grey letters
// where poppler drew patterned ones -- 31% of the pixels of the page that
// measures it.
func (r *renderer) paintCoverage(g *gstate, p *pattern, cov []float64, ox, oy, w, h int, c color.RGBA, alpha float64, resources reader.Dict) {
	if p != nil {
		// The rasteriser hands back its own scratch, which running a
		// pattern's content would write over.
		r.fillWithPattern(g, p, append([]float64{}, cov...), ox, oy, w, h, alpha, resources)
		return
	}
	r.paint(g, cov, ox, oy, w, h, c, alpha)
}

// fillWithPattern paints a coverage grid with a pattern rather than a colour.
func (r *renderer) fillWithPattern(g *gstate, p *pattern, cov []float64, ox, oy, w, h int, alpha float64, resources reader.Dict) {
	// A pattern is placed in the page's own space, not in whatever transform
	// is in force where it is used: its own matrix maps pattern space onto
	// that, and the page's own transform maps it onto the image.
	m := r.base.Mul(p.matrix)
	if p.shading != nil {
		r.paintShading(g, p.shading, m, cov, ox, oy, w, h, alpha)
		return
	}
	r.tile(g, p, m, cov, ox, oy, w, h, alpha)
}

// maxTiles bounds how many times one pattern may be drawn, so that a pattern
// whose step is tiny cannot ask for the whole afternoon.
const maxTiles = 4096

// tile runs a tiling pattern's content once for every place it lands inside
// the shape being filled.
func (r *renderer) tile(g *gstate, p *pattern, m geometry.Matrix, cov []float64, ox, oy, w, h int, alpha float64) {
	if r.depth >= maxFormDepth {
		return
	}
	inv, ok := m.Invert()
	if !ok {
		return
	}
	// Which tiles can reach the box being filled: the box's corners, in the
	// pattern's own space, say how far the tiling has to run.
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		q := inv.TransformPoint(geometry.Point{
			X: float64(ox) + c[0]*float64(w),
			Y: float64(oy) + c[1]*float64(h),
		})
		minX, minY = math.Min(minX, q.X), math.Min(minY, q.Y)
		maxX, maxY = math.Max(maxX, q.X), math.Max(maxY, q.Y)
	}
	xstep, ystep := math.Abs(p.xstep), math.Abs(p.ystep)
	if xstep < 1e-6 || ystep < 1e-6 {
		return
	}
	i0 := int(math.Floor((minX - p.bbox[2]) / xstep))
	i1 := int(math.Ceil((maxX - p.bbox[0]) / xstep))
	j0 := int(math.Floor((minY - p.bbox[3]) / ystep))
	j1 := int(math.Ceil((maxY - p.bbox[1]) / ystep))
	if (i1-i0+1)*(j1-j0+1) > maxTiles {
		return
	}
	// Everything the tiles draw goes through the shape being filled, so the
	// clip is narrowed to it first. The pattern's content begins from a clean
	// colour — black, in no pattern — because a pattern is not drawn inside
	// itself: leaving the pattern in force would have every fill in its own
	// cell start the whole thing again.
	inner := g.clone()
	r.narrow(&inner, cov, ox, oy, w, h, true)
	inner.fillAlpha, inner.strokeAlpha = alpha, alpha
	inner.fill, inner.stroke = color.RGBA{A: 255}, color.RGBA{A: 255}
	inner.fillSpace, inner.strokeSpace = deviceGray, deviceGray
	inner.fillPattern, inner.strokePattern = nil, nil
	// A pattern's content runs in the DEFAULT graphics state, not in the one
	// that used it (8.7.3.1: "the graphics state shall be initialised to its
	// default values" -- the transform and what it is drawn through
	// excepted). The colour above is that rule already; the text state is the
	// same rule and was missing.
	//
	// What it costs: a page that sets `2 Tr` -- fill AND stroke -- before
	// drawing text through a pattern had every glyph of the PATTERN stroked
	// as well, in a colour the pattern never set. Measured on the page that
	// draws an X and an O through a tiling pattern of red letters: 1349
	// glyphs painted red and 1372 painted black over them, and the page came
	// out with no red in it at all where poppler drew 4% of its pixels red.
	inner.text = textState{}
	inner.lineWidth, inner.miterLimit = 1, 10
	inner.lineCap, inner.lineJoin = 0, 0
	inner.dash, inner.dashPhase = nil, 0
	if p.uncoloured {
		inner.fill, inner.stroke = p.colour, p.colour
		inner.fillSpace, inner.strokeSpace = deviceRGB, deviceRGB
		inner.fixedColour = true
	}
	r.depth++
	defer func() { r.depth-- }()
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			cell := inner.clone()
			cell.ctm = m.Mul(geometry.Matrix{Xx: 1, Yy: 1,
				X0: float64(i) * xstep, Y0: float64(j) * ystep})
			r.run(p.content, p.resources, cell)
		}
	}
}
