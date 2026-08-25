package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

// drawXObject handles Do, which draws something named in the resources: a form
// — a piece of content with its own transform and resources — or an image,
// which a later wave will decode.
func (r *renderer) drawXObject(g *gstate, operands []reader.Object, resources reader.Dict) {
	if len(operands) == 0 || r.depth >= maxFormDepth {
		return
	}
	name, ok := reader.ToName(operands[0])
	if !ok {
		return
	}
	xobjects, ok := r.doc.GetDict(resources, "XObject")
	if !ok {
		return
	}
	stream, ok := reader.ToStream(resolve(r.doc, xobjects.Get(name)))
	if !ok {
		return
	}
	if sub, _ := reader.ToName(resolve(r.doc, stream.Dict.Get("Subtype"))); sub != "Form" {
		return
	}
	r.drawForm(g, stream, resources)
}

// drawForm runs a form's own content inside the state that drew it, under its
// own matrix and its own bounding box.
func (r *renderer) drawForm(g *gstate, stream *reader.Stream, parent reader.Dict) {
	content, img, err := r.doc.DecodeStream(stream)
	if err != nil || img != "" {
		return
	}
	inner := g.clone()
	if arr, ok := reader.ToArray(resolve(r.doc, stream.Dict.Get("Matrix"))); ok && len(arr) == 6 {
		n := make([]float64, 6)
		for i := range n {
			v, ok := reader.ToFloat(resolve(r.doc, arr[i]))
			if !ok {
				return
			}
			n[i] = v
		}
		inner.ctm = inner.ctm.Mul(matrix(n))
	}
	// A form's bounding box clips what it draws, which files rely on.
	if box, ok := rectangle(r.doc, stream.Dict.Get("BBox")); ok {
		r.clipToBox(&inner, box)
	}
	resources, ok := r.doc.GetDict(stream.Dict, "Resources")
	if !ok {
		resources = parent
	}
	r.depth++
	r.run(content, resources, inner)
	r.depth--
}

// clipToBox narrows the clip to a rectangle in the current user space.
func (r *renderer) clipToBox(g *gstate, box [4]float64) {
	path := newRectPath(g.ctm, box)
	cov, ox, oy, w, h, ok := r.rz.Fill(path, 0, r.img.W, r.img.H)
	r.narrow(g, cov, ox, oy, w, h, ok)
}

// newRectPath is the four corners of a rectangle, transformed.
func newRectPath(m geometry.Matrix, box [4]float64) *vector.Path {
	p := vector.NewPath()
	corners := [][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[2], box[3]}, {box[0], box[3]}}
	for i, c := range corners {
		pt := m.TransformPoint(geometry.Point{X: c[0], Y: c[1]})
		if i == 0 {
			p.MoveTo(pt.X, pt.Y)
			continue
		}
		p.LineTo(pt.X, pt.Y)
	}
	p.Close()
	return p
}
