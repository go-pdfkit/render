package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/pdffont"
	"github.com/go-pdfkit/reader"
	"math"
)

// A textState is everything a show operator reads besides the string itself.
type textState struct {
	font      *pdfFont
	size      float64
	charSpace float64
	wordSpace float64
	// scale is the horizontal stretch, as a fraction rather than the
	// percentage the operator takes.
	scale   float64
	leading float64
	rise    float64
	mode    int
}

// The text rendering modes that matter to what appears on the page.
const (
	modeFill = iota
	modeStroke
	modeFillStroke
	modeInvisible
)

// runText handles every operator between BT and ET, and the two that set the
// text state from outside it.
func (r *renderer) runText(g *gstate, op string, operands []reader.Object, resources reader.Dict) {
	n := numbers(operands)
	switch op {
	case "BT":
		r.tm, r.tlm = geometry.Identity(), geometry.Identity()
	case "ET":
	case "Tf":
		if len(operands) >= 2 {
			g.text.font = r.font(operands[0], resources)
			g.text.size, _ = reader.ToFloat(operands[1])
		}
	case "Tc":
		if len(n) >= 1 {
			g.text.charSpace = n[0]
		}
	case "Tw":
		if len(n) >= 1 {
			g.text.wordSpace = n[0]
		}
	case "Tz":
		if len(n) >= 1 {
			g.text.scale = n[0] / 100
		}
	case "TL":
		if len(n) >= 1 {
			g.text.leading = n[0]
		}
	case "Ts":
		if len(n) >= 1 {
			g.text.rise = n[0]
		}
	case "Tr":
		if len(n) >= 1 {
			g.text.mode = int(n[0])
		}
	case "Td":
		if len(n) >= 2 {
			r.nextLine(n[0], n[1])
		}
	case "TD":
		if len(n) >= 2 {
			g.text.leading = -n[1]
			r.nextLine(n[0], n[1])
		}
	case "Tm":
		if len(n) >= 6 {
			r.tm = matrix(n)
			r.tlm = r.tm
		}
	case "T*":
		r.nextLine(0, -g.text.leading)
	case "Tj":
		if len(operands) >= 1 {
			r.showOperand(g, operands[0], resources)
		}
	case "'":
		r.nextLine(0, -g.text.leading)
		if len(operands) >= 1 {
			r.showOperand(g, operands[0], resources)
		}
	case "\"":
		if len(operands) >= 3 {
			g.text.wordSpace, _ = reader.ToFloat(operands[0])
			g.text.charSpace, _ = reader.ToFloat(operands[1])
			r.nextLine(0, -g.text.leading)
			r.showOperand(g, operands[2], resources)
		}
	case "TJ":
		if len(operands) >= 1 {
			r.showArray(g, operands[0], resources)
		}
	}
}

// nextLine moves the text position, which is what every positioning operator
// comes down to.
func (r *renderer) nextLine(tx, ty float64) {
	r.tlm = r.tlm.Mul(geometry.Translate(tx, ty))
	r.tm = r.tlm
}

// showOperand draws one string.
func (r *renderer) showOperand(g *gstate, o reader.Object, resources reader.Dict) {
	s, ok := reader.ToString(o)
	if !ok {
		return
	}
	r.show(g, s, resources)
}

// showArray draws the strings of a TJ array, moving the pen back by the
// numbers between them.
func (r *renderer) showArray(g *gstate, o reader.Object, resources reader.Dict) {
	arr, ok := reader.ToArray(o)
	if !ok {
		return
	}
	for _, e := range arr {
		if s, ok := reader.ToString(e); ok {
			r.show(g, s, resources)
			continue
		}
		if v, ok := reader.ToFloat(e); ok {
			// A number moves the pen by that many thousandths of the size,
			// backwards, which is how a file tightens or loosens its spacing.
			r.tm = r.tm.Mul(geometry.Translate(-v/1000*g.text.size*g.text.scale, 0))
		}
	}
}

// show draws one string and leaves the pen where the next one starts.
func (r *renderer) show(g *gstate, s []byte, resources reader.Dict) {
	f := g.text.font
	if f == nil || g.text.size == 0 {
		return
	}
	if g.text.scale == 0 {
		g.text.scale = 1
	}
	single := f.Kind() != pdffont.Composite
	for _, code := range f.Codes(s) {
		if g.text.mode != modeInvisible && !r.suppressed() {
			r.drawGlyph(g, f, code, resources)
		}
		advance := (f.advance(code)*g.text.size + g.text.charSpace) * g.text.scale
		if single && code == ' ' {
			advance += g.text.wordSpace * g.text.scale
		}
		r.tm = r.tm.Mul(geometry.Translate(advance, 0))
	}
}

// glyphMatrix maps a glyph's own coordinates onto the page: the size, the
// stretch and the rise, then where the text is, then where the page is.
func (r *renderer) glyphMatrix(g *gstate) geometry.Matrix {
	scale := geometry.Matrix{
		Xx: g.text.size * g.text.scale, Yy: g.text.size,
		Y0: g.text.rise,
	}
	return g.ctm.Mul(r.tm).Mul(scale)
}

// drawGlyph puts one glyph on the page.
func (r *renderer) drawGlyph(g *gstate, f *pdfFont, code int, resources reader.Dict) {
	if f.Kind() == pdffont.Type3 {
		r.drawType3Glyph(g, f, code, resources)
		return
	}
	segs, ok := f.glyph(code)
	if !ok || len(segs) == 0 {
		return
	}
	m := r.glyphMatrix(g).Mul(geometry.Scale(1/f.perEm, 1/f.perEm))
	if f.slant {
		// Leaning the glyph rather than the line, so that the pen goes on
		// where it was going.
		m = m.Mul(geometry.Matrix{Xx: 1, Yy: 1, Yx: fauxSlant})
	}
	// The outline stays in the glyph's own units; the device maps it by m. The
	// buffer is taken off the renderer while this glyph is drawn, because a
	// pattern painted through the glyph can show text of its own.
	path := r.glyphPath
	r.glyphPath = nil
	if path == nil {
		path = &userPath{}
	}
	path.reset()
	for _, seg := range segs {
		switch seg.Op {
		case opentype.SegMoveTo:
			path.moveTo(seg.P[0].X, seg.P[0].Y)
		case opentype.SegLineTo:
			path.lineTo(seg.P[0].X, seg.P[0].Y)
		case opentype.SegQuadTo:
			path.quadTo(seg.P[0].X, seg.P[0].Y, seg.P[1].X, seg.P[1].Y)
		case opentype.SegClose:
			path.closePath()
		}
	}
	r.paintGlyph(g, path, m, resources)
	if f.embolden {
		// A face carried in one weight is made bold by drawing its outline as
		// well as filling it.
		r.thicken(g, path, m, resources)
	}
	r.glyphPath = path
}

// thicken strokes a glyph's outline in the colour it was filled with, which is
// what makes a face carried in one weight read as bold.
func (r *renderer) thicken(g *gstate, path *userPath, m geometry.Matrix, resources reader.Dict) {
	width := fauxBoldWidth * g.text.size * emScale(g)
	if width <= 0 {
		return
	}
	r.device().embolden(g, path, m, width, resources)
}

// emScale is how many device pixels an em of text space comes to, which is
// what a width given as a fraction of the em has to be multiplied by.
func emScale(g *gstate) float64 {
	x := g.ctm.Xx*g.ctm.Xx + g.ctm.Xy*g.ctm.Xy
	y := g.ctm.Yx*g.ctm.Yx + g.ctm.Yy*g.ctm.Yy
	return math.Sqrt(math.Sqrt(x * y))
}

// paintGlyph fills or strokes a glyph, in whichever way the text state says.
func (r *renderer) paintGlyph(g *gstate, path *userPath, m geometry.Matrix, resources reader.Dict) {
	dev := r.device()
	if g.text.mode == modeFill || g.text.mode == modeFillStroke {
		dev.fillPath(g, path, m, vector.NonZero, resources)
	}
	if g.text.mode == modeStroke || g.text.mode == modeFillStroke {
		dev.strokePath(g, path, m, resources)
	}
}

// drawType3Glyph runs the little content stream that is a Type 3 glyph.
func (r *renderer) drawType3Glyph(g *gstate, f *pdfFont, code int, resources reader.Dict) {
	name, ok := f.GlyphName(code)
	if !ok || f.CharProcs() == nil || r.depth >= maxFormDepth {
		return
	}
	entry := f.CharProcs().Get(reader.Name(name))
	stream, ok := reader.ToStream(resolve(r.doc, entry))
	if !ok {
		return
	}
	// A glyph procedure that is already being drawn is a cycle, and a cycle has
	// no meaning: refuse it rather than let the depth bound turn it into 3^12
	// executions. Only a procedure named by a reference can be recognised, and
	// the number leaves the list on the way out, so showing the same glyph twice
	// in sequence is still allowed.
	if ref, isRef := entry.(reader.Ref); isRef {
		for _, num := range r.drawingProcs {
			if num == ref.Num {
				return
			}
		}
		r.drawingProcs = append(r.drawingProcs, ref.Num)
		defer func() { r.drawingProcs = r.drawingProcs[:len(r.drawingProcs)-1] }()
	}
	content, img, err := r.salvaged(stream)
	if err != nil || img != "" {
		return
	}
	inner := g.clone()
	inner.ctm = r.glyphMatrix(g).Mul(f.fontMatrix)
	res := f.Type3Resources()
	if res == nil {
		res = resources
	}
	// A glyph draws in the state that showed it, and its own text position is
	// its own business.
	tm, tlm := r.tm, r.tlm
	r.depth++
	r.run(content, res, inner)
	r.depth--
	r.tm, r.tlm = tm, tlm
}

// font finds and caches the font a name stands for.
func (r *renderer) font(o reader.Object, resources reader.Dict) *pdfFont {
	name, ok := reader.ToName(o)
	if !ok {
		return nil
	}
	entry := r.named(resources, "Font", name)
	if entry == nil {
		return nil
	}
	if ref, ok := entry.(reader.Ref); ok {
		if f, ok := r.fonts[ref.Num]; ok {
			return f
		}
		dict, ok := reader.ToDict(resolve(r.doc, entry))
		if !ok {
			return nil
		}
		f := r.loadFont(dict)
		r.fonts[ref.Num] = f
		return f
	}
	dict, ok := reader.ToDict(resolve(r.doc, entry))
	if !ok {
		return nil
	}
	return r.loadFont(dict)
}
