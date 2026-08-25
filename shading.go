package render

import (
	"image/color"
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-pdfkit/reader"
)

// A shading is a colour that varies over the page rather than standing still:
// a gradient. Two kinds carry nearly all of them — a colour that changes along
// a line, and one that changes between two circles — and both are a function
// evaluated at whatever the geometry says the point stands for.
type shading struct {
	kind   int
	space  *space
	fn     function
	coords []float64
	t0, t1 float64
	extend [2]bool
	// matrix maps the domain of a function-based shading onto the space it is
	// drawn in; the other kinds do not have one.
	matrix geometry.Matrix
	domain []float64
	bbox   []float64
	// background is what is painted where the shading says nothing, when the
	// shading names one.
	background *color.RGBA
	// mesh is set for the four kinds that carry their colours in a stream.
	mesh *mesh
}

// readShading reads a shading dictionary — or the dictionary of a shading
// stream, since the mesh kinds are streams — into something that can be asked
// for a colour at a point.
func (r *renderer) readShading(o reader.Object, resources reader.Dict) *shading {
	resolved := resolve(r.doc, o)
	dict, ok := reader.ToDict(resolved)
	if s, isStream := reader.ToStream(resolved); isStream {
		dict, ok = s.Dict, true
	}
	if !ok {
		return nil
	}
	kind, ok := reader.ToInt(resolve(r.doc, dict.Get("ShadingType")))
	if !ok {
		return nil
	}
	sh := &shading{kind: int(kind), t0: 0, t1: 1, matrix: geometry.Identity()}
	sh.space = r.colourSpace(dict.Get("ColorSpace"), resources, 0)
	if sh.space == nil || sh.space.pattern {
		return nil
	}
	if fn := r.readFunction(dict.Get("Function"), 0); fn != nil {
		sh.fn = fn
	}
	sh.coords = r.floatArray(dict.Get("Coords"))
	sh.domain = r.floatArray(dict.Get("Domain"))
	sh.bbox = r.floatArray(dict.Get("BBox"))
	if m, ok := matrixOf(r.floatArray(dict.Get("Matrix"))); ok {
		sh.matrix = m
	}
	if d := r.floatArray(dict.Get("Domain")); len(d) >= 2 && sh.kind != 1 {
		sh.t0, sh.t1 = d[0], d[1]
	}
	if e, ok := reader.ToArray(resolve(r.doc, dict.Get("Extend"))); ok && len(e) >= 2 {
		sh.extend[0], _ = reader.ToBool(resolve(r.doc, e[0]))
		sh.extend[1], _ = reader.ToBool(resolve(r.doc, e[1]))
	}
	if bg := r.floatArray(dict.Get("Background")); len(bg) > 0 {
		c := sh.space.convert(bg)
		sh.background = &c
	}
	if stream, isStream := reader.ToStream(resolved); isStream && sh.kind >= 4 && sh.kind <= 7 {
		sh.mesh = r.readMesh(sh, stream)
	}
	if !sh.usable() {
		return nil
	}
	return sh
}

// usable reports whether the shading has what its kind needs to be drawn. One
// that has not says so here rather than by drawing something wrong.
func (s *shading) usable() bool {
	switch s.kind {
	case 1:
		return s.fn != nil && s.fn.outputs() == s.space.components
	case 2:
		return s.fn != nil && len(s.coords) >= 4 && s.fn.outputs() == s.space.components
	case 3:
		return s.fn != nil && len(s.coords) >= 6 && s.fn.outputs() == s.space.components
	case 4, 5, 6, 7:
		return s.mesh != nil
	}
	return false
}

// matrixOf turns six numbers into a matrix, the way every PDF matrix is
// written.
func matrixOf(v []float64) (geometry.Matrix, bool) {
	if len(v) < 6 {
		return geometry.Identity(), false
	}
	return geometry.Matrix{Xx: v[0], Xy: v[1], Yx: v[2], Yy: v[3], X0: v[4], Y0: v[5]}, true
}

// at is the colour of the shading at a point of its own space, and false where
// the shading covers nothing.
func (s *shading) at(x, y float64) (color.RGBA, bool) {
	if !s.insideBBox(x, y) {
		return s.away()
	}
	// Only the three kinds usable reports on ever get here.
	switch s.kind {
	case 2:
		return s.axialAt(x, y)
	case 3:
		return s.radialAt(x, y)
	}
	return s.functionAt(x, y)
}

// insideBBox reports whether a point of the shading's own space is within the
// box the shading says it keeps to, when it says.
func (s *shading) insideBBox(x, y float64) bool {
	if len(s.bbox) < 4 {
		return true
	}
	return x >= math.Min(s.bbox[0], s.bbox[2]) && x <= math.Max(s.bbox[0], s.bbox[2]) &&
		y >= math.Min(s.bbox[1], s.bbox[3]) && y <= math.Max(s.bbox[1], s.bbox[3])
}

// away is what is drawn where the shading itself paints nothing.
func (s *shading) away() (color.RGBA, bool) {
	if s.background != nil {
		return *s.background, true
	}
	return color.RGBA{}, false
}

// functionAt reads a shading whose colour is simply a function of where you
// are, which is the first kind.
func (s *shading) functionAt(x, y float64) (color.RGBA, bool) {
	inv, ok := s.matrix.Invert()
	if !ok {
		return s.away()
	}
	p := inv.TransformPoint(geometry.Point{X: x, Y: y})
	d := s.domain
	if len(d) < 4 {
		d = []float64{0, 1, 0, 1}
	}
	if p.X < d[0] || p.X > d[1] || p.Y < d[2] || p.Y > d[3] {
		return s.away()
	}
	return s.space.convert(s.fn.eval([]float64{p.X, p.Y})), true
}

// axialAt reads a shading that changes along a line: where the point falls
// along that line is what decides its colour.
func (s *shading) axialAt(x, y float64) (color.RGBA, bool) {
	x0, y0, x1, y1 := s.coords[0], s.coords[1], s.coords[2], s.coords[3]
	dx, dy := x1-x0, y1-y0
	den := dx*dx + dy*dy
	if den == 0 {
		return s.colourAt(0)
	}
	t := ((x-x0)*dx + (y-y0)*dy) / den
	if t < 0 {
		if !s.extend[0] {
			return s.away()
		}
		t = 0
	}
	if t > 1 {
		if !s.extend[1] {
			return s.away()
		}
		t = 1
	}
	return s.colourAt(t)
}

// radialAt reads a shading that changes between two circles. The colour at a
// point is that of the largest circle of the family passing through it, which
// is what makes a sphere look round.
func (s *shading) radialAt(x, y float64) (color.RGBA, bool) {
	x0, y0, r0 := s.coords[0], s.coords[1], s.coords[2]
	x1, y1, r1 := s.coords[3], s.coords[4], s.coords[5]
	dx, dy, dr := x1-x0, y1-y0, r1-r0
	// The circle at parameter t is centred at (x0+t*dx, y0+t*dy) with radius
	// r0+t*dr; asking which t puts the point on that circle is a quadratic.
	fx, fy := x-x0, y-y0
	a := dx*dx + dy*dy - dr*dr
	b := fx*dx + fy*dy + r0*dr
	c := fx*fx + fy*fy - r0*r0
	var roots [2]float64
	var n int
	if math.Abs(a) < 1e-12 {
		if b == 0 {
			return s.away()
		}
		roots[0], n = c/(2*b), 1
	} else {
		disc := b*b - a*c
		if disc < 0 {
			return s.away()
		}
		root := math.Sqrt(disc)
		roots[0], roots[1] = (b+root)/a, (b-root)/a
		n = 2
	}
	// The larger root wins when both are usable, which is what makes the
	// nearer circle the visible one.
	best, found := 0.0, false
	for i := 0; i < n; i++ {
		t := roots[i]
		if r0+t*dr < 0 {
			continue
		}
		clamped := t
		if t < 0 {
			if !s.extend[0] {
				continue
			}
			clamped = 0
		}
		if t > 1 {
			if !s.extend[1] {
				continue
			}
			clamped = 1
		}
		if !found || t > best {
			best, found = clamped, true
			if t < 0 || t > 1 {
				best = clamped
			}
		}
	}
	if !found {
		return s.away()
	}
	return s.colourAt(best)
}

// colourAt evaluates the shading's function at a parameter between nothing and
// one, mapped onto whatever domain the shading named.
func (s *shading) colourAt(t float64) (color.RGBA, bool) {
	v := s.t0 + t*(s.t1-s.t0)
	return s.space.convert(s.fn.eval([]float64{v})), true
}
