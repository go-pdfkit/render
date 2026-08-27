package render

import (
	"image"
	"image/color"
	"math"

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
	switch sub, _ := reader.ToName(resolve(r.doc, stream.Dict.Get("Subtype"))); sub {
	case "Form":
		r.drawForm(g, stream, resources)
	case "Image":
		r.drawImageXObject(g, stream, resources)
	}
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
	// A form's bounding box clips what it draws, which files rely on, and it
	// is also as far as the form can reach.
	region := image.Rect(0, 0, r.img.W, r.img.H)
	if box, ok := rectangle(r.doc, stream.Dict.Get("BBox")); ok {
		r.clipToBox(&inner, box)
		region = boxBounds(inner.ctm, box, r.img.W, r.img.H)
	}
	resources, ok := r.doc.GetDict(stream.Dict, "Resources")
	if !ok {
		resources = parent
	}
	// A pattern named inside a form is placed in the form's own space, not
	// the page's: the form's matrix and the transform that drew it both count.
	// Without this a pattern used in a figure lands wherever the page's origin
	// happens to be, which is nearly always off the shape it was meant to fill.
	was := r.base
	r.base = inner.ctm
	r.depth++
	if r.isGroup(stream) && (g.fillAlpha < 1 || g.softMask != nil) {
		r.runGroup(g, content, resources, inner, region)
	} else {
		r.run(content, resources, inner)
	}
	r.depth--
	r.base = was
}

// isGroup reports whether a form says it is a transparency group, which is a
// file saying that what is inside it belongs together.
func (r *renderer) isGroup(stream *reader.Stream) bool {
	group, ok := r.doc.GetDict(stream.Dict, "Group")
	if !ok {
		return false
	}
	kind, _ := reader.ToName(resolve(r.doc, group.Get("S")))
	return kind == "Transparency"
}

// runGroup draws a transparency group as one thing. Everything inside it goes
// on at full strength against a copy of the page, and then the whole of it is
// laid over the page at the strength in force where it was used — the alpha,
// and the soft mask, and both together.
//
// The difference this makes is the difference between a shadow that fades and
// fifty overlapping shapes each fading on their own, which is what drawing the
// contents one mark at a time comes to: every overlap doubles up and the thing
// comes out darker at its seams than anywhere else.
func (r *renderer) runGroup(g *gstate, content []byte, resources reader.Dict, inner gstate, region image.Rectangle) {
	// Inside a group the alpha starts again at one and the mask is set aside,
	// since both are about to be applied to the result instead.
	inner.fillAlpha, inner.strokeAlpha, inner.softMask = 1, 1, nil
	if region.Empty() {
		return
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
	var covered []float32
	wasPainted := r.painted
	if wasPainted != nil {
		covered = make([]float32, r.img.W*r.img.H)
	}
	r.painted = covered
	r.run(content, resources, inner)
	r.painted = wasPainted

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

// boxBounds is the part of the image a rectangle in user space can reach.
func boxBounds(m geometry.Matrix, box [4]float64, w, h int) image.Rectangle {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range [][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[2], box[3]}, {box[0], box[3]}} {
		p := m.TransformPoint(geometry.Point{X: c[0], Y: c[1]})
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(image.Rect(0, 0, w, h))
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

// drawImageXObject decodes an image and puts it on the page. A stencil mask
// carries no colours of its own: it paints whatever the fill colour is, which
// is how a scanned page or a logo is drawn in one ink.
func (r *renderer) drawImageXObject(g *gstate, stream *reader.Stream, resources reader.Dict) {
	s := r.decodeImage(stream.Dict, stream.Raw, resources)
	if s == nil {
		return
	}
	if mask, ok := reader.ToBool(resolve(r.doc, stream.Dict.Get("ImageMask"))); ok && mask {
		paintStencil(s, g.fill)
	}
	r.drawImage(g, s)
}

// paintStencil fills in the colour a one-bit mask is drawn in.
func paintStencil(s *sampled, c color.RGBA) {
	for i := 0; i+3 < len(s.pix); i += 4 {
		s.pix[i], s.pix[i+1], s.pix[i+2] = c.R, c.G, c.B
	}
}

// drawInlineImage puts an image written into the content stream itself on the
// page. Its dictionary uses the abbreviated names an inline image is written
// with, which the reader expands.
func (r *renderer) drawInlineImage(g *gstate, im *reader.InlineImage, resources reader.Dict) {
	dict := im.Expanded()
	s := r.decodeImage(dict, im.Raw, resources)
	if s == nil {
		return
	}
	if mask, ok := reader.ToBool(resolve(r.doc, dict.Get("ImageMask"))); ok && mask {
		paintStencil(s, g.fill)
	}
	r.drawImage(g, s)
}
