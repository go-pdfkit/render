package render

import (
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

// A pendingClip says that the path just painted also narrows what can be seen,
// which is what W and W* mean: the clip takes effect after the paint.
type pendingClip uint8

const (
	noClip pendingClip = iota
	clipNonZero
	clipEvenOdd
)

// run executes a content stream against a graphics state.
func (r *renderer) run(content []byte, resources reader.Dict, g gstate) {
	stack := []gstate{}
	path := vector.NewPath()
	var start, current geometry.Point
	var open bool
	clip := noClip

	ops, _ := reader.Operations(content)
	for _, op := range ops {
		if r.ops++; r.ops > maxOperations {
			return
		}
		if r.overrun() {
			return
		}
		n := numbers(op.Operands)
		switch op.Operator {
		case "q":
			stack = append(stack, g.clone())
		case "Q":
			if len(stack) > 0 {
				g = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
		case "cm":
			if len(n) >= 6 {
				g.ctm = g.ctm.Mul(matrix(n))
			}
		case "w":
			if len(n) >= 1 {
				g.lineWidth = n[0]
			}
		case "J":
			if len(n) >= 1 {
				g.lineCap = lineCap(n[0])
			}
		case "j":
			if len(n) >= 1 {
				g.lineJoin = lineJoin(n[0])
			}
		case "M":
			if len(n) >= 1 {
				g.miterLimit = n[0]
			}
		case "d":
			g.dash, g.dashPhase = dashPattern(op.Operands)
		case "gs":
			r.applyExtGState(&g, op.Operands, resources)

		case "m":
			if len(n) >= 2 {
				current = r.point(&g, n[0], n[1])
				start = current
				path.MoveTo(current.X, current.Y)
				open = true
			}
		case "l":
			if len(n) >= 2 && open {
				current = r.point(&g, n[0], n[1])
				path.LineTo(current.X, current.Y)
			}
		case "c":
			if len(n) >= 6 && open {
				a := r.point(&g, n[0], n[1])
				b := r.point(&g, n[2], n[3])
				current = r.point(&g, n[4], n[5])
				path.CubicTo(a.X, a.Y, b.X, b.Y, current.X, current.Y)
			}
		case "v":
			if len(n) >= 4 && open {
				b := r.point(&g, n[0], n[1])
				end := r.point(&g, n[2], n[3])
				path.CubicTo(current.X, current.Y, b.X, b.Y, end.X, end.Y)
				current = end
			}
		case "y":
			if len(n) >= 4 && open {
				a := r.point(&g, n[0], n[1])
				end := r.point(&g, n[2], n[3])
				path.CubicTo(a.X, a.Y, end.X, end.Y, end.X, end.Y)
				current = end
			}
		case "h":
			if open {
				path.Close()
				current = start
			}
		case "re":
			if len(n) >= 4 {
				r.rectangle(&g, path, n)
				current = r.point(&g, n[0], n[1])
				start = current
				open = true
			}

		case "S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n":
			r.paintPath(&g, path, op.Operator, clip, resources)
			path = vector.NewPath()
			open = false
			clip = noClip
		case "W":
			clip = clipNonZero
		case "W*":
			clip = clipEvenOdd

		case "g", "G", "rg", "RG", "k", "K", "cs", "CS", "sc", "scn", "SC", "SCN":
			r.setColour(&g, op.Operator, op.Operands, resources)

		case "BI":
			r.drawInlineImage(&g, op.Image, resources)
		case "BT", "ET", "Tf", "Tc", "Tw", "Tz", "TL", "Ts", "Tr",
			"Td", "TD", "Tm", "T*", "Tj", "TJ", "'", "\"":
			r.runText(&g, op.Operator, op.Operands, resources)
		case "sh":
			r.drawShading(&g, op.Operands, resources)
		case "Do":
			r.drawXObject(&g, op.Operands, resources)
		}
	}
}

// point maps a pair of user-space numbers onto the image.
func (r *renderer) point(g *gstate, x, y float64) geometry.Point {
	return g.ctm.TransformPoint(geometry.Point{X: x, Y: y})
}

// rectangle adds the four corners of a re operator, which is a closed subpath
// of its own.
func (r *renderer) rectangle(g *gstate, path *vector.Path, n []float64) {
	x, y, w, h := n[0], n[1], n[2], n[3]
	corners := [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
	for i, c := range corners {
		p := r.point(g, c[0], c[1])
		if i == 0 {
			path.MoveTo(p.X, p.Y)
			continue
		}
		path.LineTo(p.X, p.Y)
	}
	path.Close()
}

// paintPath draws the path that has been built, in whichever of the ten ways
// the operator asks for, and then narrows the clip if one was pending.
func (r *renderer) paintPath(g *gstate, path *vector.Path, op string, clip pendingClip, resources reader.Dict) {
	switch op {
	case "s", "b", "b*":
		path.Close()
	}
	rule := vector.NonZero
	if op == "f*" || op == "B*" || op == "b*" {
		rule = vector.EvenOdd
	}
	if fills(op) {
		cov, ox, oy, w, h, ok := r.rz.Fill(path, rule, r.img.W, r.img.H)
		if ok {
			if g.fillPattern != nil {
				// The rasteriser hands back its own scratch, which running a
				// pattern's content would write over.
				r.fillWithPattern(g, g.fillPattern, append([]float64{}, cov...), ox, oy, w, h, g.fillAlpha, resources)
			} else {
				r.paint(g, cov, ox, oy, w, h, g.fill, g.fillAlpha)
			}
		}
	}
	if strokes(op) {
		cov, ox, oy, w, h, ok := r.rz.StrokeWith(path, r.strokeStyle(g), r.img.W, r.img.H)
		if ok {
			if g.strokePattern != nil {
				r.fillWithPattern(g, g.strokePattern, append([]float64{}, cov...), ox, oy, w, h, g.strokeAlpha, resources)
			} else {
				r.paint(g, cov, ox, oy, w, h, g.stroke, g.strokeAlpha)
			}
		}
	}
	if clip == noClip {
		return
	}
	rule = vector.NonZero
	if clip == clipEvenOdd {
		rule = vector.EvenOdd
	}
	cov, ox, oy, w, h, ok := r.rz.Fill(path, rule, r.img.W, r.img.H)
	r.narrow(g, cov, ox, oy, w, h, ok)
}

// fills reports whether a painting operator fills.
func fills(op string) bool {
	switch op {
	case "f", "F", "f*", "B", "B*", "b", "b*":
		return true
	}
	return false
}

// strokes reports whether a painting operator strokes.
func strokes(op string) bool {
	switch op {
	case "S", "s", "B", "B*", "b", "b*":
		return true
	}
	return false
}

// strokeStyle turns the graphics state into the style the rasteriser wants,
// with the width and the dash pattern carried from user space into the image.
func (r *renderer) strokeStyle(g *gstate) vector.StrokeStyle {
	s := scaleOf(g.ctm)
	width := g.lineWidth * s
	// A width of zero means the thinnest line the device can draw.
	if width < 1 {
		width = 1
	}
	dash := make([]float64, len(g.dash))
	for i, d := range g.dash {
		dash[i] = d * s
	}
	return vector.StrokeStyle{
		Width:      width,
		Cap:        g.lineCap,
		Join:       g.lineJoin,
		MiterLimit: g.miterLimit,
		Dash:       dash,
		DashPhase:  g.dashPhase * s,
	}
}

// scaleOf is how much a transform magnifies lengths, on average over
// direction, which is what a stroke width has to be scaled by when the
// transform is not the same in both axes.
func scaleOf(m geometry.Matrix) float64 {
	return math.Sqrt(math.Abs(m.Determinant()))
}

// matrix reads the six numbers of a cm operator.
func matrix(n []float64) geometry.Matrix {
	return geometry.Matrix{Xx: n[0], Xy: n[1], Yx: n[2], Yy: n[3], X0: n[4], Y0: n[5]}
}

// lineCap reads the number a J operator takes.
func lineCap(v float64) vector.LineCap {
	switch int(v) {
	case 1:
		return vector.RoundCap
	case 2:
		return vector.SquareCap
	}
	return vector.ButtCap
}

// lineJoin reads the number a j operator takes.
func lineJoin(v float64) vector.LineJoin {
	switch int(v) {
	case 1:
		return vector.RoundJoin
	case 2:
		return vector.BevelJoin
	}
	return vector.MiterJoin
}

// dashPattern reads the array and phase a d operator takes.
func dashPattern(operands []reader.Object) ([]float64, float64) {
	if len(operands) < 2 {
		return nil, 0
	}
	arr, ok := reader.ToArray(operands[0])
	if !ok {
		return nil, 0
	}
	out := make([]float64, 0, len(arr))
	for _, e := range arr {
		v, ok := reader.ToFloat(e)
		if !ok {
			return nil, 0
		}
		out = append(out, v)
	}
	phase, _ := reader.ToFloat(operands[1])
	return out, phase
}

// numbers reads the operands that are numbers, in order, stopping at the first
// that is not.
func numbers(operands []reader.Object) []float64 {
	out := make([]float64, 0, len(operands))
	for _, o := range operands {
		v, ok := reader.ToFloat(o)
		if !ok {
			return out
		}
		out = append(out, v)
	}
	return out
}
