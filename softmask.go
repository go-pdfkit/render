package render

import (
	"image/color"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// A soft mask says that what is drawn from here on shows through by an amount
// that varies from one place on the page to another. It is not a number but a
// whole little drawing of its own — a form, drawn on its own paper — and what
// is read off that drawing is the mask: either how light each pixel came out,
// or how much of it was painted at all.
//
// It is what a figure uses to fade a surface away, and it is how nearly every
// drop shadow and every soft edge in an illustration is written. A page that
// uses one and does not get it comes out with that part at full strength,
// which is worse than it sounds: a shadow meant to be a hint becomes a slab.

// maxCachedMasks bounds how many masks a page may keep drawn, since each one
// is a byte for every pixel of the page and a file may name a great many. Past
// it they are still drawn, just not kept.
const maxCachedMasks = 64

// softMaskKey is what makes two uses of a mask the same picture: the same
// mask dictionary — which is what says the form, the kind, the backdrop and
// the transfer function all at once — drawn the same way round. A file names
// one graphics state and uses it over and over, so this saves drawing the
// same mask again for each use.
type softMaskKey struct {
	mask reader.Ref
	ctm  geometry.Matrix
}

// readSoftMask builds the mask an ExtGState's /SMask names, or nil for /None
// and for anything it cannot read — in which case nothing is masked, which is
// what this package did before it could read them at all.
func (r *renderer) readSoftMask(entry reader.Object, g *gstate, resources reader.Dict) []uint8 {
	if name, ok := reader.ToName(resolve(r.doc, entry)); ok && name == "None" {
		return nil
	}
	dict, ok := reader.ToDict(resolve(r.doc, entry))
	if !ok || r.depth >= maxFormDepth {
		return nil
	}
	kind, _ := reader.ToName(resolve(r.doc, dict.Get("S")))
	if kind != "Luminosity" && kind != "Alpha" {
		return nil
	}
	form, ok := reader.ToStream(resolve(r.doc, dict.Get("G")))
	if !ok {
		return nil
	}
	// A mask written out once and pointed at is one this can recognise again;
	// one written in place is not worth telling apart from another.
	ref, cacheable := entry.(reader.Ref)
	key := softMaskKey{mask: ref, ctm: g.ctm}
	if cacheable {
		if mask, ok := r.softMasks[key]; ok {
			return mask
		}
	}
	mask := r.drawSoftMask(dict, form, kind, g, resources)
	if cacheable && len(r.softMasks) < maxCachedMasks {
		r.softMasks[key] = mask
	}
	return mask
}

// drawSoftMask draws the mask's form on paper of its own and reads the mask
// off what comes out.
func (r *renderer) drawSoftMask(dict reader.Dict, form *reader.Stream, kind reader.Name, g *gstate, resources reader.Dict) []uint8 {
	content, img, err := r.doc.DecodeStream(form)
	if err != nil || img != "" {
		return nil
	}
	backdrop := r.maskBackdrop(dict, form, kind)
	// The mask's own paper is the size of the page's, because a mask is read
	// off the same pixels the page is drawn on.
	paper := raster.New(r.img.W, r.img.H)
	fill(paper, backdrop)
	var painted []float32
	if kind == "Alpha" {
		// How much was painted is not something an opaque picture remembers,
		// so it is kept alongside.
		painted = make([]float32, r.img.W*r.img.H)
	}

	inner := gstate{
		ctm:         g.ctm,
		fill:        black,
		stroke:      black,
		fillSpace:   deviceGray,
		strokeSpace: deviceGray,
		lineWidth:   1,
		miterLimit:  10,
		fillAlpha:   1,
		strokeAlpha: 1,
	}
	if arr, ok := reader.ToArray(resolve(r.doc, form.Dict.Get("Matrix"))); ok && len(arr) == 6 {
		n := make([]float64, 6)
		for i := range n {
			v, ok := reader.ToFloat(resolve(r.doc, arr[i]))
			if !ok {
				return nil
			}
			n[i] = v
		}
		inner.ctm = inner.ctm.Mul(matrix(n))
	}
	// Everything outside the group's box is the backdrop, which the form's
	// own bounding box is what says.
	box, hasBox := rectangle(r.doc, form.Dict.Get("BBox"))

	formResources, ok := r.doc.GetDict(form.Dict, "Resources")
	if !ok {
		formResources = resources
	}

	// The form is run against its own paper, and everything the renderer
	// keeps that belongs to a page is put back afterwards. The text matrices
	// are among them: a graphics state may be named in the middle of a run of
	// text, and a mask that had any text of its own would otherwise leave the
	// pen somewhere else than it found it.
	wasImg, wasPainted, wasBase := r.img, r.painted, r.base
	wasTm, wasTlm := r.tm, r.tlm
	r.img, r.painted, r.base = paper, painted, inner.ctm
	if hasBox {
		r.clipToBox(&inner, box)
	}
	r.depth++
	r.run(content, formResources, inner)
	r.depth--
	r.img, r.painted, r.base = wasImg, wasPainted, wasBase
	r.tm, r.tlm = wasTm, wasTlm

	mask := make([]uint8, r.img.W*r.img.H)
	if kind == "Alpha" {
		for i, v := range painted {
			mask[i] = byteOf(float64(v))
		}
	} else {
		for i := range mask {
			mask[i] = byteOf(luminosity(paper.Pix[i*4], paper.Pix[i*4+1], paper.Pix[i*4+2]))
		}
	}
	r.applyTransfer(mask, dict)
	return mask
}

// black is what a mask's paper starts as, and what masks everything the
// drawing on it does not reach.
var black = color.RGBA{A: 255}

// maskBackdrop is what the mask's paper starts as. For a mask read from the
// light in a drawing that is the backdrop colour the file names, or black,
// which masks everything the drawing does not reach; for one read from how
// much was painted the paper does not matter, since nothing was.
func (r *renderer) maskBackdrop(dict reader.Dict, form *reader.Stream, kind reader.Name) color.RGBA {
	if kind == "Alpha" {
		return black
	}
	bc := r.floatArray(dict.Get("BC"))
	if len(bc) == 0 {
		return black
	}
	group, ok := r.doc.GetDict(form.Dict, "Group")
	if !ok {
		return black
	}
	// A backdrop colour is written in the group's own colour space, so a
	// group that names none has nothing to write it in.
	cs, named := group["CS"]
	if !named {
		return black
	}
	sp := r.colourSpace(cs, nil, 0)
	if sp == nil || sp.pattern || len(bc) < sp.components {
		return black
	}
	return sp.convert(bc)
}

// luminosity is how light a colour is, weighted the way an eye weights it,
// which is what the format says a mask of this kind reads.
func luminosity(r8, g8, b8 uint8) float64 {
	return (0.3*float64(r8) + 0.59*float64(g8) + 0.11*float64(b8)) / 255
}

// applyTransfer runs the mask through the function the file names, which is
// how a mask is made harder or softer than the drawing it came from.
func (r *renderer) applyTransfer(mask []uint8, dict reader.Dict) {
	entry := resolve(r.doc, dict.Get("TR"))
	if name, ok := reader.ToName(entry); ok && name == "Identity" {
		return
	}
	fn := r.readFunction(dict.Get("TR"), 0)
	if fn == nil || fn.outputs() < 1 {
		return
	}
	// A transfer function takes one number and gives one back, so the whole
	// of it is 256 values and every pixel is a lookup.
	var table [256]uint8
	for i := range table {
		out := fn.eval([]float64{float64(i) / 255})
		table[i] = byteOf(out[0])
	}
	for i, v := range mask {
		mask[i] = table[v]
	}
}
