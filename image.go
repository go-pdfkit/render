package render

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg" // the one image format a PDF may carry undecoded
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// A sampled image is what a PDF image XObject or an inline image comes down
// to: a grid of colours, some of which may be see-through.
type sampled struct {
	w, h int
	// pix is four bytes a pixel, straight alpha, the same shape as a raster
	// image so that drawing it is a matter of sampling.
	pix []uint8
}

// at reads one pixel.
func (s *sampled) at(x, y int) color.RGBA {
	i := (y*s.w + x) * 4
	return color.RGBA{R: s.pix[i], G: s.pix[i+1], B: s.pix[i+2], A: s.pix[i+3]}
}

// drawImage puts an image XObject on the page. A PDF image occupies the unit
// square of the current user space, whichever way round that has been turned,
// so the transform is inverted and the image sampled through it — which is
// what makes a rotated or mirrored image come out right.
func (r *renderer) drawImage(g *gstate, s *sampled) {
	inv, ok := g.ctm.Invert()
	if !ok {
		return // the unit square has been squashed to nothing
	}
	box := unitSquareBounds(g.ctm, r.img.W, r.img.H)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			// The middle of the device pixel, in the image's own coordinates.
			p := inv.TransformPoint(geometry.Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
			if p.X < 0 || p.X >= 1 || p.Y < 0 || p.Y >= 1 {
				continue
			}
			sx := int(p.X * float64(s.w))
			// The unit square counts up from its bottom and an image counts
			// down from its top row, and counting down from the last row keeps
			// the index inside the image whatever the fraction rounds to.
			sy := s.h - 1 - int(p.Y*float64(s.h))
			c := s.at(sx, sy)
			alpha := float64(c.A) / 255 * g.fillAlpha
			if g.clip != nil {
				alpha *= g.clip.at(x, y)
			}
			if g.softMask != nil {
				alpha *= maskLevel(g.softMask[y*r.img.W+x])
			}
			if alpha <= 0 {
				continue
			}
			r.img.Set(x, y, blend(r.img.At(x, y), c, alpha))
			r.markPixel(x, y, alpha)
		}
	}
}

// blend puts one colour over another.
func blend(under, over color.RGBA, alpha float64) color.RGBA {
	mix := func(a, b uint8) uint8 {
		return uint8(math.Round(float64(a)*(1-alpha) + float64(b)*alpha))
	}
	return color.RGBA{
		R: mix(under.R, over.R),
		G: mix(under.G, over.G),
		B: mix(under.B, over.B),
		A: 255,
	}
}

// unitSquareBounds is the part of the image the unit square can reach.
func unitSquareBounds(m geometry.Matrix, w, h int) image.Rectangle {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		p := m.TransformPoint(geometry.Point{X: c[0], Y: c[1]})
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	r := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1)
	return r.Intersect(image.Rect(0, 0, w, h))
}

// maxImagePixels bounds how large an image may be decoded to, so a file cannot
// name a grid it would take the whole machine to hold.
const maxImagePixels = 64 << 20

// decodeImage turns an image XObject into a grid of colours.
func (r *renderer) decodeImage(dict reader.Dict, raw []byte, resources reader.Dict) *sampled {
	w := int(intOr(resolve(r.doc, dict.Get("Width")), 0))
	h := int(intOr(resolve(r.doc, dict.Get("Height")), 0))
	if w <= 0 || h <= 0 || w*h > maxImagePixels {
		return nil
	}
	// An image whose filter chain broke part way gives the rows it managed,
	// which is what a viewer shows for a truncated scan. Bytes no filter
	// decoded are in a field this cannot reach.
	dec := reader.DecodeRecovering(dict, raw, r.doc.Resolver())
	data, imageFilter := dec.Data, dec.Image
	if len(data) == 0 {
		return nil
	}
	if mask, ok := reader.ToBool(resolve(r.doc, dict.Get("ImageMask"))); ok && mask {
		// A stencil is one bit a pixel, so the bytes have to be samples. When
		// the filter chain stopped at an image format nothing here decodes,
		// they are not: they are still compressed, and drawing them paints
		// noise through the shape of nothing.
		//
		// 273 of the image masks in the 1 633 real forms carry an encoded
		// filter. 236 of them were faxes, which the reader now decodes; the
		// nine that remain are JBIG2, and until something decodes those the
		// honest answer is not to draw them. That is the rule the rest of this
		// function already follows: "the image is not drawn rather than drawn
		// wrong".
		if imageFilter != "" {
			return nil
		}
		return r.stencil(dict, data, w, h)
	}
	var out *sampled
	switch imageFilter {
	case "":
		out = r.samples(dict, data, w, h, resources)
	case "DCTDecode", "DCT":
		out = decodeJPEG(data, w, h)
	default:
		// A format nothing here can decode: the image is not drawn rather than
		// drawn wrong.
		return nil
	}
	if out == nil {
		return nil
	}
	r.applyTransparency(out, dict, resources)
	return out
}

// stencil reads a one-bit image mask, which paints the fill colour through its
// own shape and lets everything else show through.
func (r *renderer) stencil(dict reader.Dict, data []byte, w, h int) *sampled {
	// A stencil is drawn in the colour in force, which the caller has already
	// put in the graphics state; the sampled image carries only its shape, and
	// the colour is filled in by the caller.
	invert := false
	if arr, ok := reader.ToArray(resolve(r.doc, dict.Get("Decode"))); ok && len(arr) >= 1 {
		if v, ok := reader.ToFloat(resolve(r.doc, arr[0])); ok && v == 1 {
			invert = true
		}
	}
	out := &sampled{w: w, h: h, pix: make([]uint8, w*h*4)}
	rowBytes := (w + 7) / 8
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*rowBytes + x/8
			bit := byte(0)
			if i < len(data) {
				bit = data[i] >> (7 - x%8) & 1
			}
			on := bit == 0
			if invert {
				on = !on
			}
			if on {
				out.pix[(y*w+x)*4+3] = 255
			}
		}
	}
	return out
}

// samples reads an image whose pixels are numbers in a colour space.
func (r *renderer) samples(dict reader.Dict, data []byte, w, h int, resources reader.Dict) *sampled {
	bpc := int(intOr(resolve(r.doc, dict.Get("BitsPerComponent")), 8))
	if bpc != 1 && bpc != 2 && bpc != 4 && bpc != 8 && bpc != 16 {
		return nil
	}
	sp := r.colourSpace(dict.Get("ColorSpace"), resources, 0)
	n := sp.components
	decode := r.decodeArray(dict, sp, bpc)
	out := &sampled{w: w, h: h, pix: make([]uint8, w*h*4)}
	rowBits := w * n * bpc
	rowBytes := (rowBits + 7) / 8
	comps := make([]float64, n)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < n; c++ {
				raw := sampleAt(data, y*rowBytes, (x*n+c)*bpc, bpc)
				comps[c] = decode(c, raw, bpc)
			}
			col := sp.convert(comps)
			i := (y*w + x) * 4
			out.pix[i], out.pix[i+1], out.pix[i+2], out.pix[i+3] = col.R, col.G, col.B, 255
		}
	}
	return out
}

// decodeArray gives the function that turns a raw sample into the number a
// colour space wants, honouring a /Decode array when the file has one.
func (r *renderer) decodeArray(dict reader.Dict, sp *space, bpc int) func(c int, raw uint32, bits int) float64 {
	maxValue := float64(uint32(1)<<bpc - 1)
	arr, ok := reader.ToArray(resolve(r.doc, dict.Get("Decode")))
	if !ok || len(arr) < 2*sp.components {
		if sp.name == "Indexed" {
			// An indexed image's samples are row numbers, not fractions.
			return func(_ int, raw uint32, _ int) float64 { return float64(raw) }
		}
		return func(_ int, raw uint32, _ int) float64 { return float64(raw) / maxValue }
	}
	bounds := make([]float64, 2*sp.components)
	for i := range bounds {
		v, ok := reader.ToFloat(resolve(r.doc, arr[i]))
		if !ok {
			return func(_ int, raw uint32, _ int) float64 { return float64(raw) / maxValue }
		}
		bounds[i] = v
	}
	return func(c int, raw uint32, _ int) float64 {
		lo, hi := bounds[2*c], bounds[2*c+1]
		return lo + float64(raw)*(hi-lo)/maxValue
	}
}

// sampleAt reads one sample of the given width out of a row of packed bits.
func sampleAt(data []byte, rowStart, bitOffset, bpc int) uint32 {
	if bpc == 8 {
		i := rowStart + bitOffset/8
		if i >= len(data) {
			return 0
		}
		return uint32(data[i])
	}
	if bpc == 16 {
		i := rowStart + bitOffset/8
		if i+1 >= len(data) {
			return 0
		}
		return uint32(data[i])<<8 | uint32(data[i+1])
	}
	var v uint32
	for k := 0; k < bpc; k++ {
		bit := bitOffset + k
		i := rowStart + bit/8
		b := byte(0)
		if i < len(data) {
			b = data[i] >> (7 - bit%8) & 1
		}
		v = v<<1 | uint32(b)
	}
	return v
}

// decodeJPEG reads the one compressed image format a PDF may carry whole.
func decodeJPEG(data []byte, w, h int) *sampled {
	img, err := jpegDecode(data)
	if err != nil {
		return nil
	}
	b := img.Bounds()
	if b.Dx() != w || b.Dy() != h {
		w, h = b.Dx(), b.Dy()
	}
	src := raster.FromImage(img)
	return &sampled{w: w, h: h, pix: src.Pix}
}

// jpegDecode is a variable so a test can watch what happens when a decoder
// refuses what it is given.
var jpegDecode = func(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// applyTransparency reads whichever of the two ways a PDF says which parts of
// an image are see-through: a soft mask of its own, or a range of colours to
// treat as absent.
func (r *renderer) applyTransparency(s *sampled, dict reader.Dict, resources reader.Dict) {
	if stream, ok := reader.ToStream(resolve(r.doc, dict.Get("SMask"))); ok {
		r.applySoftMask(s, stream, resources)
		return
	}
	maskEntry := resolve(r.doc, dict.Get("Mask"))
	if stream, ok := reader.ToStream(maskEntry); ok {
		r.applyStencilMask(s, stream, resources)
	}
}

// applySoftMask reads a grey image whose levels say how much of each pixel
// shows.
func (r *renderer) applySoftMask(s *sampled, stream *reader.Stream, resources reader.Dict) {
	mask := r.decodeImage(stream.Dict, stream.Raw, resources)
	if mask == nil {
		return
	}
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			m := mask.at(x*mask.w/s.w, y*mask.h/s.h)
			s.pix[(y*s.w+x)*4+3] = m.R
		}
	}
}

// applyStencilMask reads a one-bit image whose set pixels are the ones to
// leave out.
func (r *renderer) applyStencilMask(s *sampled, stream *reader.Stream, resources reader.Dict) {
	mask := r.decodeImage(stream.Dict, stream.Raw, resources)
	if mask == nil {
		return
	}
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			m := mask.at(x*mask.w/s.w, y*mask.h/s.h)
			// A stencil mask marks what is hidden, so where it paints, the
			// image does not.
			if m.A > 127 {
				s.pix[(y*s.w+x)*4+3] = 0
			}
		}
	}
}

// intOr reads an integer, or gives a default.
func intOr(o reader.Object, def int64) int64 {
	if v, ok := reader.ToInt(o); ok {
		return v
	}
	return def
}
