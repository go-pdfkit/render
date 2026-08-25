package render

import "image/color"

// A patch is the sixth and seventh kinds of shading: four curved sides and a
// colour at each corner. The sides are cubic Bézier curves, so a patch is
// twelve control points round the edge — and, for the seventh kind, four more
// inside that let the surface bulge where the edges alone would not.
//
// Nothing can fill a curved-sided patch directly, so it is cut into a grid of
// small quadrilaterals, each of them two triangles, and every corner takes its
// colour from where it sits in the patch.
type patch struct {
	// p is the control net: p[i][j], where i runs with one parameter of the
	// surface and j with the other.
	p [4][4]point2
	// c is the colour at each corner, going round the patch from p[0][0] in
	// the order the format writes them.
	c [4]color.RGBA
}

// A point2 is a place in the shading's own space.
type point2 struct{ x, y float64 }

// The order the twelve boundary points arrive in, as positions in the control
// net: round the patch, starting at one corner.
var boundaryOrder = [12][2]int{
	{0, 0}, {0, 1}, {0, 2}, {0, 3},
	{1, 3}, {2, 3}, {3, 3},
	{3, 2}, {3, 1}, {3, 0},
	{2, 0}, {1, 0},
}

// The four inner points a tensor patch adds, in the order they arrive.
var innerOrder = [4][2]int{{1, 1}, {1, 2}, {2, 2}, {2, 1}}

// The edge of the previous patch that a flag says this one shares, as the four
// control points it stands in for. A flag of one means the previous patch's
// far edge, two the one after that, three the one after that again; those
// become this patch's first four boundary points.
var sharedEdge = [4][4][2]int{
	1: {{0, 3}, {1, 3}, {2, 3}, {3, 3}},
	2: {{3, 3}, {3, 2}, {3, 1}, {3, 0}},
	3: {{3, 0}, {2, 0}, {1, 0}, {0, 0}},
}

// The two colours that edge brings with it, as positions in the previous
// patch's colour list.
var sharedColours = [4][2]int{1: {1, 2}, 2: {2, 3}, 3: {3, 0}}

// patches reads a type 6 or type 7 mesh and cuts every patch into triangles.
func (r *meshReader) patches(m *mesh, kind int) {
	tensor := kind == 7
	var previous *patch
	for !r.done() && len(m.triangles) < maxMeshTriangles {
		r.align()
		flag := int(r.read(r.bits.flag)) & 3
		if r.bad {
			return
		}
		if flag != 0 && previous == nil {
			// A patch that carries on from one that is not there.
			return
		}
		p := &patch{}
		start := 0
		if flag != 0 {
			for i, at := range sharedEdge[flag] {
				side := boundaryOrder[i]
				p.p[side[0]][side[1]] = previous.p[at[0]][at[1]]
			}
			p.c[0] = previous.c[sharedColours[flag][0]]
			p.c[1] = previous.c[sharedColours[flag][1]]
			start = 4
		}
		for i := start; i < 12; i++ {
			at := boundaryOrder[i]
			p.p[at[0]][at[1]] = point2{r.coordinate(0), r.coordinate(1)}
		}
		if tensor {
			for _, at := range innerOrder {
				p.p[at[0]][at[1]] = point2{r.coordinate(0), r.coordinate(1)}
			}
		} else {
			p.fillCoonsInside()
		}
		for i := start / 2; i < 4; i++ {
			p.c[i] = r.colour()
		}
		if r.bad {
			return
		}
		p.cutUp(m)
		previous = p
	}
}

// fillCoonsInside works out the four inner control points a Coons patch does
// not carry: its inside follows from its edges, and this is the combination
// that says how. The same shape serves all four, with the indices turned
// round, because a patch is symmetric in both directions.
func (p *patch) fillCoonsInside() {
	p.p[1][1] = p.coonsInner([2]int{0, 0}, [2]int{0, 1}, [2]int{1, 0}, [2]int{0, 3}, [2]int{3, 0}, [2]int{3, 1}, [2]int{1, 3}, [2]int{3, 3})
	p.p[1][2] = p.coonsInner([2]int{0, 3}, [2]int{0, 2}, [2]int{1, 3}, [2]int{0, 0}, [2]int{3, 3}, [2]int{3, 2}, [2]int{1, 0}, [2]int{3, 0})
	p.p[2][1] = p.coonsInner([2]int{3, 0}, [2]int{3, 1}, [2]int{2, 0}, [2]int{3, 3}, [2]int{0, 0}, [2]int{0, 1}, [2]int{2, 3}, [2]int{0, 3})
	p.p[2][2] = p.coonsInner([2]int{3, 3}, [2]int{3, 2}, [2]int{2, 3}, [2]int{3, 0}, [2]int{0, 3}, [2]int{0, 2}, [2]int{2, 0}, [2]int{0, 0})
}

// coonsInner is the inner point nearest one corner: the corner itself, the two
// boundary points beside it, the two corners along from it, the two boundary
// points nearest those on the far edges, and the opposite corner.
func (p *patch) coonsInner(corner, a, b, along1, along2, far1, far2, opposite [2]int) point2 {
	at := func(i [2]int) point2 { return p.p[i[0]][i[1]] }
	mix := func(get func(point2) float64) float64 {
		return (-4*get(at(corner)) +
			6*(get(at(a))+get(at(b))) -
			2*(get(at(along1))+get(at(along2))) +
			3*(get(at(far1))+get(at(far2))) -
			get(at(opposite))) / 9
	}
	return point2{
		x: mix(func(q point2) float64 { return q.x }),
		y: mix(func(q point2) float64 { return q.y }),
	}
}

// cutUp turns the patch into triangles: a grid of small quadrilaterals, each
// of them two, with every corner coloured by where it sits.
func (p *patch) cutUp(m *mesh) {
	var grid [patchSteps + 1][patchSteps + 1]vertex
	for i := 0; i <= patchSteps; i++ {
		u := float64(i) / patchSteps
		for j := 0; j <= patchSteps; j++ {
			v := float64(j) / patchSteps
			at := p.surface(u, v)
			grid[i][j] = vertex{x: at.x, y: at.y, c: p.colourAt(u, v)}
		}
	}
	for i := 0; i < patchSteps; i++ {
		for j := 0; j < patchSteps; j++ {
			m.add(grid[i][j], grid[i+1][j], grid[i][j+1])
			m.add(grid[i+1][j], grid[i+1][j+1], grid[i][j+1])
		}
	}
}

// surface is where the point (u,v) of the patch lands: the control net read as
// a cubic Bézier surface.
func (p *patch) surface(u, v float64) point2 {
	bu := bernstein(u)
	bv := bernstein(v)
	var out point2
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			w := bu[i] * bv[j]
			out.x += w * p.p[i][j].x
			out.y += w * p.p[i][j].y
		}
	}
	return out
}

// bernstein is the four weights a cubic curve gives at a parameter.
func bernstein(t float64) [4]float64 {
	s := 1 - t
	return [4]float64{s * s * s, 3 * s * s * t, 3 * s * t * t, t * t * t}
}

// colourAt mixes the four corner colours by where the point sits. The corners
// go round the patch rather than across it, so the two along one side are the
// first and the last.
func (p *patch) colourAt(u, v float64) color.RGBA {
	mix := func(get func(color.RGBA) uint8) uint8 {
		near := float64(get(p.c[0]))*(1-v) + float64(get(p.c[1]))*v
		far := float64(get(p.c[3]))*(1-v) + float64(get(p.c[2]))*v
		return byteOf((near*(1-u) + far*u) / 255)
	}
	return color.RGBA{
		R: mix(func(c color.RGBA) uint8 { return c.R }),
		G: mix(func(c color.RGBA) uint8 { return c.G }),
		B: mix(func(c color.RGBA) uint8 { return c.B }),
		A: 255,
	}
}
