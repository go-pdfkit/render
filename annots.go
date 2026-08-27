package render

import (
	"maps"
	"math"
	"slices"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-pdfkit/reader"
)

// A page's content is not all of it. Beside it the page carries annotations:
// a link, a note somebody left, a highlight, a rubber stamp — and the widgets
// that are a form's fields, which is where the value somebody typed into a
// form actually shows.
//
// Each says how it looks in an appearance stream of its own, which is a form
// XObject like any other, and drawing a page without them leaves a filled-in
// form blank. That is not a small thing: a tax return drawn without its
// annotations is a tax return with nothing in it.
//
// The corpus holds 84 302 links, 397 squares, 58 pieces of free text and a
// scattering of the rest; almost none of the links carry an appearance, since
// a link is a place to click rather than a thing to draw.

// drawAnnotations puts a page's annotations on it, after its content, which is
// the order they go in.
func (r *renderer) drawAnnotations(page reader.Dict, base gstate) {
	annots, ok := reader.ToArray(resolve(r.doc, page.Get("Annots")))
	if !ok {
		return
	}
	for _, entry := range annots {
		dict, ok := reader.ToDict(resolve(r.doc, entry))
		if !ok {
			continue
		}
		if r.skipAnnotation(dict) {
			continue
		}
		stream, ok := r.appearanceOf(dict)
		if !ok {
			continue
		}
		rect, ok := rectangle(r.doc, dict.Get("Rect"))
		if !ok {
			continue
		}
		r.drawAppearance(base, stream, rect)
	}
}

// The flags a page's annotation carries. The second says it is not to be shown
// at all and the sixth that it is not to be shown on a screen; a popup is the
// little window a note opens into, which is not part of the page.
const (
	annotHidden = 1 << 1
	annotNoView = 1 << 5
)

// skipAnnotation says whether an annotation is one the page does not show.
func (r *renderer) skipAnnotation(dict reader.Dict) bool {
	if sub, _ := reader.ToName(resolve(r.doc, dict.Get("Subtype"))); sub == "Popup" {
		return true
	}
	// An annotation carries its own /OC, which is how a whole block of a
	// form's fields is put on a layer. 1 373 annotations on the first three
	// pages of the 1 633 real forms name one — though none of those names a
	// layer that is off, so nothing in the corpus is hidden by this line.
	if entry, named := dict["OC"]; named && r.oc.hidden(r.doc, entry) {
		return true
	}
	flags, _ := reader.ToInt(resolve(r.doc, dict.Get("F")))
	return flags&annotHidden != 0 || flags&annotNoView != 0
}

// appearanceOf finds the drawing an annotation shows. It may be one stream, or
// a set of them with a name for each state the thing can be in — which is how
// a checkbox carries both a ticked and an unticked picture, and /AS says which
// of them it is in now.
func (r *renderer) appearanceOf(dict reader.Dict) (*reader.Stream, bool) {
	ap, ok := r.doc.GetDict(dict, "AP")
	if !ok {
		return nil, false
	}
	normal := resolve(r.doc, ap.Get("N"))
	if stream, ok := reader.ToStream(normal); ok {
		return stream, true
	}
	states, ok := reader.ToDict(normal)
	if !ok {
		return nil, false
	}
	if name, ok := reader.ToName(resolve(r.doc, dict.Get("AS"))); ok {
		if stream, ok := reader.ToStream(resolve(r.doc, states.Get(name))); ok {
			return stream, true
		}
		// A widget whose state names a picture it has not got shows nothing,
		// which is what an unticked box with only a ticked picture is.
		return nil, false
	}
	// With no state named, a set of one is unambiguous and anything more is
	// the file not saying which it means.
	if len(states) != 1 {
		return nil, false
	}
	only := slices.Collect(maps.Keys(states))[0]
	stream, ok := reader.ToStream(resolve(r.doc, states.Get(only)))
	return stream, ok
}

// drawAppearance draws one annotation's stream into the rectangle the page
// gives it.
//
// An appearance is drawn in a space of its own, which its bounding box
// describes and its matrix may turn. What the page says is where the result
// has to end up. So the box is turned by the matrix, the smallest rectangle
// round what comes out is worked out, and that is stretched onto the page's
// rectangle — which is what makes a stamp put down at an angle land inside the
// box that was drawn for it.
func (r *renderer) drawAppearance(base gstate, stream *reader.Stream, rect [4]float64) {
	content, img, err := r.salvaged(stream)
	if err != nil || img != "" {
		return
	}
	box, ok := rectangle(r.doc, stream.Dict.Get("BBox"))
	if !ok {
		return
	}
	form := geometry.Identity()
	if arr, ok := reader.ToArray(resolve(r.doc, stream.Dict.Get("Matrix"))); ok && len(arr) == 6 {
		n := make([]float64, 6)
		for i := range n {
			v, ok := reader.ToFloat(resolve(r.doc, arr[i]))
			if !ok {
				return
			}
			n[i] = v
		}
		form = matrix(n)
	}
	fit, ok := fitBox(form, box, rect)
	if !ok {
		return
	}

	inner := base.clone()
	inner.ctm = base.ctm.Mul(fit).Mul(form)
	r.clipToBox(&inner, box)

	resources, _ := r.doc.GetDict(stream.Dict, "Resources")
	was := r.base
	r.base = inner.ctm
	r.depth++
	r.run(content, resources, inner)
	r.depth--
	r.base = was
}

// fitBox is the transform that puts a turned bounding box inside the rectangle
// the page gave the annotation: what a reader has to work out for itself,
// since the file says only where the thing goes and not how to get it there.
func fitBox(m geometry.Matrix, box, rect [4]float64) (geometry.Matrix, bool) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range [][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[2], box[3]}, {box[0], box[3]}} {
		p := m.TransformPoint(geometry.Point{X: c[0], Y: c[1]})
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	w, h := maxX-minX, maxY-minY
	// A box that has been squashed to nothing cannot be stretched onto
	// anything, and one whose numbers are not numbers is a file being wrong.
	if math.IsNaN(w) || math.IsNaN(h) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return geometry.Identity(), false
	}
	sx, sy := 1.0, 1.0
	if w > 0 {
		sx = (rect[2] - rect[0]) / w
	}
	if h > 0 {
		sy = (rect[3] - rect[1]) / h
	}
	return geometry.Matrix{
		Xx: sx, Xy: 0,
		Yx: 0, Yy: sy,
		X0: rect[0] - minX*sx,
		Y0: rect[1] - minY*sy,
	}, true
}
