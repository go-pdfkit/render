package render

import (
	"image"
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

// A device is where the marks a content stream makes end up.
//
// The interpreter (exec.go, text.go, xobject.go) works out WHAT is drawn: which
// path, under which transform, in which graphics state. A device decides what
// drawing it means. Everything crossing this interface is in user space: a path
// comes with the matrix that maps it onto the page, never already mapped, so a
// device that keeps geometry (an SVG writer) gets it untransformed, and a device
// that rasterises maps it itself.
//
// The colour, the alpha, the pattern, the stroke parameters, the clip and the
// soft mask are read from the graphics state each method is given.
type device interface {
	// fillPath fills p, mapped by m, with the fill colour of g.
	fillPath(g *gstate, p *userPath, m geometry.Matrix, rule vector.FillRule, resources reader.Dict)
	// strokePath strokes p, mapped by m, with the stroke colour and the line
	// parameters of g.
	strokePath(g *gstate, p *userPath, m geometry.Matrix, resources reader.Dict)
	// embolden strokes p in the FILL colour with a round pen whose width is
	// given in device pixels: how a face carried in one weight is made bold.
	embolden(g *gstate, p *userPath, m geometry.Matrix, width float64, resources reader.Dict)
	// clipPath narrows the clip of g to p, mapped by m. rect is the path's own
	// x, y, width, height when isRect says it was exactly one rectangle, which a
	// device may use to clip without building the shape. They are values, not a
	// pointer, because a pointer given to an interface method escapes.
	clipPath(g *gstate, p *userPath, m geometry.Matrix, rule vector.FillRule, rect [4]float64, isRect bool)
	// image paints a decoded picture, straight alpha, over the unit square of
	// g's transform.
	image(g *gstate, s *sampled)
	// shade is the sh operator: a shading over everything the clip allows.
	shade(g *gstate, sh *shading)
	// beginGroup opens a transparency group: a form whose content is to be
	// composed as one thing. ctm is the transform inside the form and box its
	// bounding box in that space, when hasBox says it has one. ok is false when the
	// group can reach nothing, and nothing is to be run. endGroup closes the
	// group opened last and composes the result at the alpha and soft mask of g;
	// groups nest, so a device keeps them as a stack.
	beginGroup(g *gstate, ctm geometry.Matrix, box [4]float64, hasBox bool) (ok bool)
	endGroup(g *gstate)
}

// device is the one in force, which is a rasteriser unless the renderer was
// given another.
func (r *renderer) device() device {
	if r.dev == nil {
		r.dev = rasterDevice{r}
	}
	return r.dev
}

// segOp is one command of a userPath.
type segOp uint8

const (
	segMove segOp = iota
	segLine
	segQuad
	segCubic
	segClose
)

// A userSeg is one command and its points: one for a move or a line, two for a
// quadratic (control, end), three for a cubic (two controls, end).
type userSeg struct {
	op segOp
	p  [3]geometry.Point
}

// A userPath is an outline in the space it was written in. It is the path a
// content stream builds, kept until the operator that paints it arrives.
//
// It is reused from one painting operator to the next, and a device that needs
// the path mapped to the image asks for it once per matrix: a path that is both
// filled and clipped is mapped once.
type userPath struct {
	segs []userSeg

	// mapped is segs through mappedBy, and holds only while valid: any change
	// to segs clears it. It is a value, not a pointer, so that mapping a path
	// costs no allocation beyond the segments themselves.
	mapped   vector.Path
	mappedBy geometry.Matrix
	valid    bool
}

func (u *userPath) add(s userSeg) {
	u.segs = append(u.segs, s)
	u.valid = false
}

func (u *userPath) moveTo(x, y float64) {
	u.add(userSeg{op: segMove, p: [3]geometry.Point{{X: x, Y: y}}})
}

func (u *userPath) lineTo(x, y float64) {
	u.add(userSeg{op: segLine, p: [3]geometry.Point{{X: x, Y: y}}})
}

func (u *userPath) quadTo(cx, cy, x, y float64) {
	u.add(userSeg{op: segQuad, p: [3]geometry.Point{{X: cx, Y: cy}, {X: x, Y: y}}})
}

func (u *userPath) cubicTo(a, b, c geometry.Point) {
	u.add(userSeg{op: segCubic, p: [3]geometry.Point{a, b, c}})
}

func (u *userPath) closePath() { u.add(userSeg{op: segClose}) }

// reset empties the path and keeps its storage.
func (u *userPath) reset() {
	u.segs = u.segs[:0]
	u.valid = false
}

// device is the path with every point carried through m, which is the shape a
// rasteriser wants. The answer is kept for the next call with the same matrix.
func (u *userPath) device(m geometry.Matrix) *vector.Path {
	if u.valid && u.mappedBy == m {
		return &u.mapped
	}
	u.mapped = vector.Path{}
	out := &u.mapped
	for _, s := range u.segs {
		switch s.op {
		case segMove:
			p := m.TransformPoint(s.p[0])
			out.MoveTo(p.X, p.Y)
		case segLine:
			p := m.TransformPoint(s.p[0])
			out.LineTo(p.X, p.Y)
		case segQuad:
			c, p := m.TransformPoint(s.p[0]), m.TransformPoint(s.p[1])
			out.QuadTo(c.X, c.Y, p.X, p.Y)
		case segCubic:
			a, b, p := m.TransformPoint(s.p[0]), m.TransformPoint(s.p[1]), m.TransformPoint(s.p[2])
			out.CubicTo(a.X, a.Y, b.X, b.Y, p.X, p.Y)
		case segClose:
			out.Close()
		}
	}
	u.mappedBy, u.valid = m, true
	return out
}

// A rasterDevice paints into the renderer's image: the coverage grids, the clip
// chain, the soft masks and the pixel-copy groups all live on the renderer, and
// this is the one place the interpreter reaches them from.
type rasterDevice struct{ r *renderer }

func (d rasterDevice) fillPath(g *gstate, p *userPath, m geometry.Matrix, rule vector.FillRule, resources reader.Dict) {
	r := d.r
	cov, ox, oy, w, h, ok := r.rz.Fill(p.device(m), rule, r.img.W, r.img.H)
	if ok {
		r.paintCoverage(g, g.fillPattern, cov, ox, oy, w, h, g.fill, g.fillAlpha, resources)
	}
}

func (d rasterDevice) strokePath(g *gstate, p *userPath, m geometry.Matrix, resources reader.Dict) {
	r := d.r
	cov, ox, oy, w, h, ok := r.rz.StrokeWith(p.device(m), r.strokeStyle(g), r.img.W, r.img.H)
	if ok {
		r.paintCoverage(g, g.strokePattern, cov, ox, oy, w, h, g.stroke, g.strokeAlpha, resources)
	}
}

func (d rasterDevice) embolden(g *gstate, p *userPath, m geometry.Matrix, width float64, resources reader.Dict) {
	r := d.r
	style := vector.StrokeStyle{Width: width, Cap: vector.RoundCap, Join: vector.RoundJoin}
	if cov, ox, oy, w, h, ok := r.rz.StrokeWith(p.device(m), style, r.img.W, r.img.H); ok {
		r.paintCoverage(g, g.fillPattern, cov, ox, oy, w, h, g.fill, g.fillAlpha, resources)
	}
}

func (d rasterDevice) clipPath(g *gstate, p *userPath, m geometry.Matrix, rule vector.FillRule, rect [4]float64, isRect bool) {
	r := d.r
	if isRect {
		if dr := r.deviceRect(g, rect[:]); dr != nil {
			// The shape is a rectangle and was known to be one before it was
			// built, so it need not be rasterised to be clipped with.
			ox, oy, w, h, ok := dr.Box(r.img.W, r.img.H)
			r.narrowRect(g, *dr, ox, oy, w, h, ok)
			return
		}
	}
	cov, ox, oy, w, h, ok := r.rz.Fill(p.device(m), rule, r.img.W, r.img.H)
	r.narrow(g, cov, ox, oy, w, h, ok)
}

func (d rasterDevice) image(g *gstate, s *sampled) { d.r.drawImage(g, s) }

func (d rasterDevice) shade(g *gstate, sh *shading) {
	r := d.r
	cov, ox, oy, w, h := r.wholeClip(g)
	r.paintShading(g, sh, g.ctm, cov, ox, oy, w, h, g.fillAlpha)
}

// A rasterGroup is what a group keeps while its content runs: the part of the
// page it can reach as it was before, and the bookkeeping of the soft mask that
// may be asking how much of the page was marked.
type rasterGroup struct {
	region     image.Rectangle
	before     []uint8
	covered    []float32
	wasPainted []float32
}

func (d rasterDevice) beginGroup(g *gstate, ctm geometry.Matrix, box [4]float64, hasBox bool) bool {
	r := d.r
	region := image.Rect(0, 0, r.img.W, r.img.H)
	if hasBox {
		region = boxBounds(ctm, box, r.img.W, r.img.H)
	}
	if region.Empty() {
		return false
	}
	// Only the part of the page the form's own box can reach is kept and put
	// back, since that is as far as anything inside it can draw.
	w, h := region.Dx(), region.Dy()
	before := make([]uint8, w*h*4)
	for y := 0; y < h; y++ {
		from := ((region.Min.Y+y)*r.img.W + region.Min.X) * 4
		copy(before[y*w*4:(y+1)*w*4], r.img.Pix[from:from+w*4])
	}
	// How much of the group reached each pixel, kept only when this group is
	// itself inside a mask that asks that question.
	grp := rasterGroup{region: region, before: before, wasPainted: r.painted}
	if grp.wasPainted != nil {
		grp.covered = make([]float32, r.img.W*r.img.H)
	}
	r.painted = grp.covered
	r.groups = append(r.groups, grp)
	return true
}

func (d rasterDevice) endGroup(g *gstate) {
	r := d.r
	grp := r.groups[len(r.groups)-1]
	r.groups = r.groups[:len(r.groups)-1]
	r.painted = grp.wasPainted
	region, before, covered, wasPainted := grp.region, grp.before, grp.covered, grp.wasPainted
	w, h := region.Dx(), region.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px, py := region.Min.X+x, region.Min.Y+y
			i := py*r.img.W + px
			a := g.fillAlpha
			if g.softMask != nil {
				a *= maskLevel(g.softMask[i])
			}
			if wasPainted != nil {
				r.markPixel(px, py, float64(covered[i])*a)
			}
			if a >= 1 {
				continue
			}
			was := before[(y*w+x)*4:]
			if a <= 0 {
				copy(r.img.Pix[i*4:i*4+4], was[:4])
				continue
			}
			for c := 0; c < 3; c++ {
				k := i*4 + c
				r.img.Pix[k] = uint8(math.Round(float64(was[c])*(1-a) + float64(r.img.Pix[k])*a))
			}
		}
	}
}
