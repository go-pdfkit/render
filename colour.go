package render

import (
	"image/color"

	"github.com/go-pdfkit/reader"
)

// setColour handles every operator that changes what colour things are drawn
// in. The upper-case ones set the stroking colour and the lower-case ones the
// filling colour, which is a rule the whole format keeps to.
func (r *renderer) setColour(g *gstate, op string, operands []reader.Object, resources reader.Dict) {
	if g.fixedColour {
		// Inside an uncoloured pattern: the shape is the pattern's, the
		// colour is the page's, and a colour operator here would paint over
		// what the page asked for — with white, more often than not.
		return
	}
	stroking := op == "G" || op == "RG" || op == "K" || op == "CS" || op == "SC" || op == "SCN"
	n := numbers(operands)
	switch op {
	case "g", "G":
		r.assign(g, stroking, deviceGray, n)
	case "rg", "RG":
		r.assign(g, stroking, deviceRGB, n)
	case "k", "K":
		r.assign(g, stroking, deviceCMYK, n)
	case "cs", "CS":
		if len(operands) == 0 {
			return
		}
		s := r.colourSpace(operands[0], resources, 0)
		r.setSpace(g, stroking, s)
	case "sc", "scn", "SC", "SCN":
		s := g.fillSpace
		if stroking {
			s = g.strokeSpace
		}
		if s.pattern {
			r.setPattern(g, stroking, operands, n, resources)
			return
		}
		r.assign(g, stroking, s, n)
	}
}

// setPattern names what a mark in the Pattern space is drawn with. The last
// operand is the pattern's name; anything in front of it is the colour an
// uncoloured pattern draws in.
func (r *renderer) setPattern(g *gstate, stroking bool, operands []reader.Object, n []float64, resources reader.Dict) {
	var p *pattern
	if len(operands) > 0 {
		if name, ok := reader.ToName(operands[len(operands)-1]); ok {
			p = r.readPattern(name, resources)
		}
	}
	if p != nil && p.uncoloured {
		// The numbers in front of the name are in the space the pattern was
		// declared under, which the Pattern space names as its own.
		under := g.fillSpace
		if stroking {
			under = g.strokeSpace
		}
		p.colour = color.RGBA{A: 255}
		if under.under != nil {
			p.colour = under.under.convert(n)
		}
	}
	// A pattern that cannot be read leaves a mid grey rather than nothing, so
	// that what was drawn does not vanish.
	c := color.RGBA{R: 128, G: 128, B: 128, A: 255}
	if stroking {
		g.strokePattern, g.stroke = p, c
		return
	}
	g.fillPattern, g.fill = p, c
}

// setSpace changes the space and resets the colour to that space's black,
// which is what the specification requires of cs and CS.
func (r *renderer) setSpace(g *gstate, stroking bool, s *space) {
	if stroking {
		g.strokeSpace, g.stroke, g.strokePattern = s, s.initial(), nil
		return
	}
	g.fillSpace, g.fill, g.fillPattern = s, s.initial(), nil
}

// assign sets both the space and the colour, which is what the operators that
// name a space and its numbers in one go do.
func (r *renderer) assign(g *gstate, stroking bool, s *space, n []float64) {
	c := s.convert(n)
	if stroking {
		g.strokeSpace, g.stroke, g.strokePattern = s, c, nil
		return
	}
	g.fillSpace, g.fill, g.fillPattern = s, c, nil
}

// applyExtGState reads the parameters a gs operator names. Only the ones that
// change what this package already draws are read; the rest are left for the
// waves that will need them.
func (r *renderer) applyExtGState(g *gstate, operands []reader.Object, resources reader.Dict) {
	if len(operands) == 0 {
		return
	}
	name, ok := reader.ToName(operands[0])
	if !ok {
		return
	}
	states, ok := r.doc.GetDict(resources, "ExtGState")
	if !ok {
		return
	}
	params, ok := r.doc.GetDict(states, name)
	if !ok {
		return
	}
	if v, ok := reader.ToFloat(resolve(r.doc, params.Get("ca"))); ok {
		g.fillAlpha = clamp01(v)
	}
	if v, ok := reader.ToFloat(resolve(r.doc, params.Get("CA"))); ok {
		g.strokeAlpha = clamp01(v)
	}
	if v, ok := reader.ToFloat(resolve(r.doc, params.Get("LW"))); ok {
		g.lineWidth = v
	}
	if v, ok := reader.ToFloat(resolve(r.doc, params.Get("LC"))); ok {
		g.lineCap = lineCap(v)
	}
	if v, ok := reader.ToFloat(resolve(r.doc, params.Get("LJ"))); ok {
		g.lineJoin = lineJoin(v)
	}
	if v, ok := reader.ToFloat(resolve(r.doc, params.Get("ML"))); ok {
		g.miterLimit = v
	}
	if arr, ok := reader.ToArray(resolve(r.doc, params.Get("D"))); ok && len(arr) == 2 {
		g.dash, g.dashPhase = dashPattern([]reader.Object{resolve(r.doc, arr[0]), resolve(r.doc, arr[1])})
	}
}
