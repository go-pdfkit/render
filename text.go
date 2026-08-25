package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/reader"
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
	single := f.kind != compositeFont
	for _, code := range f.codes(s) {
		if g.text.mode != modeInvisible {
			r.drawGlyph(g, f, code, resources)
		}
		advance := (f.width(code)*g.text.size + g.text.charSpace) * g.text.scale
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
	if f.kind == type3Font {
		r.drawType3Glyph(g, f, code, resources)
		return
	}
	segs, ok := f.glyph(code)
	if !ok || len(segs) == 0 {
		return
	}
	m := r.glyphMatrix(g).Mul(geometry.Scale(1/f.perEm, 1/f.perEm))
	path := vector.NewPath()
	at := func(p opentype.Point) geometry.Point {
		return m.TransformPoint(geometry.Point{X: p.X, Y: p.Y})
	}
	for _, seg := range segs {
		switch seg.Op {
		case opentype.SegMoveTo:
			p := at(seg.P[0])
			path.MoveTo(p.X, p.Y)
		case opentype.SegLineTo:
			p := at(seg.P[0])
			path.LineTo(p.X, p.Y)
		case opentype.SegQuadTo:
			c, p := at(seg.P[0]), at(seg.P[1])
			path.QuadTo(c.X, c.Y, p.X, p.Y)
		case opentype.SegClose:
			path.Close()
		}
	}
	r.paintGlyph(g, path)
}

// paintGlyph fills or strokes a glyph, in whichever way the text state says.
func (r *renderer) paintGlyph(g *gstate, path *vector.Path) {
	if g.text.mode == modeFill || g.text.mode == modeFillStroke {
		if cov, ox, oy, w, h, ok := r.rz.Fill(path, vector.NonZero, r.img.W, r.img.H); ok {
			r.paint(g, cov, ox, oy, w, h, g.fill, g.fillAlpha)
		}
	}
	if g.text.mode == modeStroke || g.text.mode == modeFillStroke {
		if cov, ox, oy, w, h, ok := r.rz.StrokeWith(path, r.strokeStyle(g), r.img.W, r.img.H); ok {
			r.paint(g, cov, ox, oy, w, h, g.stroke, g.strokeAlpha)
		}
	}
}

// drawType3Glyph runs the little content stream that is a Type 3 glyph.
func (r *renderer) drawType3Glyph(g *gstate, f *pdfFont, code int, resources reader.Dict) {
	name, ok := f.names[code]
	if !ok || f.charProcs == nil || r.depth >= maxFormDepth {
		return
	}
	stream, ok := reader.ToStream(resolve(r.doc, f.charProcs.Get(reader.Name(name))))
	if !ok {
		return
	}
	content, img, err := r.doc.DecodeStream(stream)
	if err != nil || img != "" {
		return
	}
	inner := g.clone()
	inner.ctm = r.glyphMatrix(g).Mul(f.fontMatrix)
	res := f.t3Resources
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
	fonts, ok := r.doc.GetDict(resources, "Font")
	if !ok {
		return nil
	}
	entry := fonts.Get(name)
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
