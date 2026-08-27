package render

import (
	"image/color"
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-pdfkit/reader"
)

// The four mesh shadings are the ones that carry their colours in a stream
// rather than in a function: a page says where the corners are and what
// colour each is, and the shape between them is filled in.
//
// Types 4 and 5 are triangles — free-form, where every vertex says how it
// joins the ones before it, and lattice-form, where they come in rows of a
// stated length. Types 6 and 7 are patches with curved sides: a Coons patch,
// whose inside follows from its twelve boundary points, and a tensor patch,
// which says four more points for the inside as well. This draws all four,
// and draws a patch by cutting it into small quadrilaterals, which is what
// makes a curved-sided patch a thing that can be filled at all.
type mesh struct {
	triangles []triangle
}

// A triangle is three corners, each with a colour of its own, in the
// shading's own space.
type triangle struct {
	x, y [3]float64
	c    [3]color.RGBA
}

// A vertex is one corner as the stream holds it.
type vertex struct {
	x, y float64
	c    color.RGBA
}

// maxMeshTriangles bounds how much a file may ask to be drawn, so that a
// stream naming a million patches cannot ask for the afternoon.
const maxMeshTriangles = 1 << 20

// patchSteps is how finely a curved-sided patch is cut up. Sixteen along each
// side is past the point where a step shows at any size a page is looked at,
// and is 512 triangles a patch.
const patchSteps = 16

// readMesh reads the vertices or patches a mesh shading's stream holds.
func (r *renderer) readMesh(sh *shading, stream *reader.Stream) *mesh {
	data, img, err := r.salvaged(stream)
	if err != nil || img != "" {
		return nil
	}
	if sh.fn != nil && sh.fn.outputs() != sh.space.components {
		return nil
	}
	bits := r.meshBits(stream.Dict, sh)
	if bits == nil {
		return nil
	}
	rd := &meshReader{data: data, bits: *bits, sh: sh}
	m := &mesh{}
	switch sh.kind {
	case 4:
		rd.freeTriangles(m)
	case 5:
		rd.latticeTriangles(m, bits.perRow)
	case 6, 7:
		rd.patches(m, sh.kind)
	}
	if len(m.triangles) == 0 {
		return nil
	}
	return m
}

// meshBits is how wide each number in the stream is, and what range each maps
// onto.
type meshBits struct {
	coord, comp, flag int
	decode            []float64
	// components is how many numbers a colour takes in the stream: one when
	// the shading names a function, which turns that one into a colour, and
	// otherwise as many as its space has.
	components int
	perRow     int
}

// meshBits reads the widths and the decode array a mesh stream is written
// with, and refuses one that says something it cannot mean.
func (r *renderer) meshBits(dict reader.Dict, sh *shading) *meshBits {
	b := &meshBits{components: sh.space.components}
	if sh.fn != nil {
		b.components = 1
	}
	b.coord = int(intOr(resolve(r.doc, dict.Get("BitsPerCoordinate")), 0))
	switch b.coord {
	case 1, 2, 4, 8, 12, 16, 24, 32:
	default:
		return nil
	}
	b.comp = int(intOr(resolve(r.doc, dict.Get("BitsPerComponent")), 0))
	switch b.comp {
	case 1, 2, 4, 8, 12, 16:
	default:
		return nil
	}
	if sh.kind != 5 {
		b.flag = int(intOr(resolve(r.doc, dict.Get("BitsPerFlag")), 0))
		switch b.flag {
		case 2, 4, 8:
		default:
			return nil
		}
	} else {
		b.perRow = int(intOr(resolve(r.doc, dict.Get("VerticesPerRow")), 0))
		if b.perRow < 2 || b.perRow > 1<<16 {
			return nil
		}
	}
	b.decode = r.floatArray(dict.Get("Decode"))
	if len(b.decode) < 4+2*b.components {
		return nil
	}
	return b
}

// A meshReader walks the packed stream a bit at a time.
type meshReader struct {
	data []byte
	at   int // in bits
	bits meshBits
	sh   *shading
	bad  bool
}

// done reports whether there is nothing left worth reading.
func (r *meshReader) done() bool { return r.bad || r.at >= len(r.data)*8 }

// read takes one number of the given width.
func (r *meshReader) read(width int) uint64 {
	if r.at+width > len(r.data)*8 {
		r.bad = true
		return 0
	}
	var v uint64
	for k := 0; k < width; k++ {
		i := r.at + k
		v = v<<1 | uint64(r.data[i/8]>>(7-i%8)&1)
	}
	r.at += width
	return v
}

// align moves to the next byte, which is where every vertex and every patch
// begins.
func (r *meshReader) align() {
	if r.at%8 != 0 {
		r.at += 8 - r.at%8
	}
}

// coordinate reads one packed number and maps it onto what the decode array
// says it means.
func (r *meshReader) coordinate(i int) float64 {
	raw := r.read(r.bits.coord)
	max := float64(uint64(1)<<uint(r.bits.coord) - 1)
	return interpolate(float64(raw), 0, max, r.bits.decode[2*i], r.bits.decode[2*i+1])
}

// colour reads a vertex's colour, through the shading's function when it has
// one.
func (r *meshReader) colour() color.RGBA {
	comps := make([]float64, r.bits.components)
	max := float64(uint64(1)<<uint(r.bits.comp) - 1)
	for i := range comps {
		raw := r.read(r.bits.comp)
		lo, hi := r.bits.decode[4+2*i], r.bits.decode[5+2*i]
		comps[i] = interpolate(float64(raw), 0, max, lo, hi)
	}
	if r.sh.fn != nil {
		return r.sh.space.convert(r.sh.fn.eval(comps))
	}
	return r.sh.space.convert(comps)
}

// vertex reads one corner: where it is and what colour it is.
func (r *meshReader) vertex() vertex {
	x := r.coordinate(0)
	y := r.coordinate(1)
	return vertex{x: x, y: y, c: r.colour()}
}

// freeTriangles reads a type 4 mesh, where every vertex says how it joins the
// two before it: begin a triangle, carry the last two on, or carry the first
// and the last.
func (r *meshReader) freeTriangles(m *mesh) {
	var a, b, c vertex
	have := 0
	for !r.done() && len(m.triangles) < maxMeshTriangles {
		r.align()
		flag := int(r.read(r.bits.flag))
		v := r.vertex()
		if r.bad {
			return
		}
		switch {
		case flag == 0 || have < 3:
			// A new triangle: this vertex and the two that follow.
			if flag == 0 && have >= 3 {
				have = 0
			}
			switch have {
			case 0:
				a, have = v, 1
			case 1:
				b, have = v, 2
			default:
				c, have = v, 3
				m.add(a, b, c)
			}
		case flag == 1:
			a, b, c = b, c, v
			m.add(a, b, c)
		default:
			b, c = c, v
			m.add(a, b, c)
		}
	}
}

// latticeTriangles reads a type 5 mesh: rows of a stated length, with two
// triangles between every pair of neighbours in consecutive rows.
func (r *meshReader) latticeTriangles(m *mesh, perRow int) {
	var previous []vertex
	for !r.done() && len(m.triangles) < maxMeshTriangles {
		row := make([]vertex, 0, perRow)
		for i := 0; i < perRow; i++ {
			row = append(row, r.vertex())
		}
		if r.bad {
			return
		}
		if previous != nil {
			for i := 0; i+1 < perRow; i++ {
				m.add(previous[i], previous[i+1], row[i])
				m.add(previous[i+1], row[i+1], row[i])
			}
		}
		previous = row
	}
}

// add puts one triangle in the mesh.
func (m *mesh) add(a, b, c vertex) {
	m.triangles = append(m.triangles, triangle{
		x: [3]float64{a.x, b.x, c.x},
		y: [3]float64{a.y, b.y, c.y},
		c: [3]color.RGBA{a.c, b.c, c.c},
	})
}

// A meshRaster is a mesh drawn into device pixels: the colour of every pixel
// the mesh covers, and nothing where it covers none. A mesh is the one kind of
// shading that cannot be asked what colour a point is without first working
// out which triangle the point is in, so it is drawn once and then read.
type meshRaster struct {
	ox, oy, w, h int
	// col holds one colour a pixel, with a zero alpha where the mesh does not
	// reach; every colour it does set is opaque.
	col []color.RGBA
}

// at is the colour of one device pixel, and false where the mesh covers none.
func (m *meshRaster) at(x, y int) (color.RGBA, bool) {
	i := (y-m.oy)*m.w + (x - m.ox)
	if i < 0 || i >= len(m.col) || m.col[i].A == 0 {
		return color.RGBA{}, false
	}
	return m.col[i], true
}

// rasterise draws every triangle of the mesh into a patch of device pixels.
// Neighbouring triangles agree along the edge they share, so where two of them
// both claim a pixel it does not matter which one wins.
func (m *mesh) rasterise(t geometry.Matrix, ox, oy, w, h int) *meshRaster {
	out := &meshRaster{ox: ox, oy: oy, w: w, h: h, col: make([]color.RGBA, w*h)}
	for i := range m.triangles {
		out.draw(&m.triangles[i], t)
	}
	return out
}

// draw puts one triangle down, taking each pixel's colour from where it sits
// between the three corners.
func (r *meshRaster) draw(t *triangle, m geometry.Matrix) {
	var px, py [3]float64
	for i := 0; i < 3; i++ {
		p := m.TransformPoint(geometry.Point{X: t.x[i], Y: t.y[i]})
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			return
		}
		px[i], py[i] = p.X, p.Y
	}
	area := (px[1]-px[0])*(py[2]-py[0]) - (px[2]-px[0])*(py[1]-py[0])
	if area == 0 {
		return // a triangle with no inside covers nothing
	}
	loX := max(r.ox, int(math.Floor(min3(px))))
	hiX := min(r.ox+r.w, int(math.Ceil(max3(px)))+1)
	loY := max(r.oy, int(math.Floor(min3(py))))
	hiY := min(r.oy+r.h, int(math.Ceil(max3(py)))+1)
	// A pixel exactly on a shared edge belongs to both triangles that meet
	// there; letting it in on both sides is what keeps a seam from showing.
	const inside = -1e-9
	for y := loY; y < hiY; y++ {
		for x := loX; x < hiX; x++ {
			cx, cy := float64(x)+0.5, float64(y)+0.5
			w0 := ((px[1]-cx)*(py[2]-cy) - (px[2]-cx)*(py[1]-cy)) / area
			w1 := ((px[2]-cx)*(py[0]-cy) - (px[0]-cx)*(py[2]-cy)) / area
			w2 := 1 - w0 - w1
			if w0 < inside || w1 < inside || w2 < inside {
				continue
			}
			r.col[(y-r.oy)*r.w+(x-r.ox)] = mixThree(t.c, w0, w1, w2)
		}
	}
}

// mixThree is the colour a point takes from the three corners around it.
func mixThree(c [3]color.RGBA, w0, w1, w2 float64) color.RGBA {
	part := func(get func(color.RGBA) uint8) uint8 {
		v := w0*float64(get(c[0])) + w1*float64(get(c[1])) + w2*float64(get(c[2]))
		return byteOf(v / 255)
	}
	return color.RGBA{
		R: part(func(c color.RGBA) uint8 { return c.R }),
		G: part(func(c color.RGBA) uint8 { return c.G }),
		B: part(func(c color.RGBA) uint8 { return c.B }),
		A: 255,
	}
}

// min3 and max3 are the ends of a triangle's reach along one axis.
func min3(v [3]float64) float64 { return math.Min(v[0], math.Min(v[1], v[2])) }
func max3(v [3]float64) float64 { return math.Max(v[0], math.Max(v[1], v[2])) }
