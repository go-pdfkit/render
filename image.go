package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg" // the one image format a PDF may carry undecoded
	"math"

	jpeg2000 "github.com/ajroetker/go-jpeg2000"
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

// decodeImage turns an image XObject into the grid of colours a page draws:
// the picture the codec read, with whatever mask it names applied to it.
func (r *renderer) decodeImage(dict reader.Dict, raw []byte, resources reader.Dict) *sampled {
	out := r.decodeBase(dict, raw, resources)
	if out == nil {
		return nil
	}
	if !r.applyTransparency(out, dict, resources) {
		// A mask was named and could not be read, so how much of this image
		// shows is unknown. Drawing it whole is the worst of the three
		// answers: it is how a scanned page's high-resolution ink layer, which
		// is meant to show through a stencil, ends up painted over the page as
		// a solid dark rectangle.
		return nil
	}
	return out
}

// decodeBase is the picture the codec read, with no mask applied.
//
// It is separate because the two questions are different. What a page draws
// is the composited picture; what a codec produced is this one, and comparing
// a codec against another implementation means comparing THIS, since the other
// implementation hands its masks back separately too. Measured the wrong way
// round, 21 of 22 JPEG 2000 pictures in a corpus of scanned pages looked
// wrong, and 11 of them differed only by an /SMask that had been applied to
// one side and not the other.
func (r *renderer) decodeBase(dict reader.Dict, raw []byte, resources reader.Dict) *sampled {
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
	if imageFilter == "JBIG2Decode" {
		// Decoded here rather than in the switch below because a JBIG2 stream
		// is far more often a stencil than an image, and a stencil never
		// reaches that switch.
		if data = r.decodeJBIG2(dict, data, w, h); data == nil {
			return nil
		}
		imageFilter = ""
	}
	if mask, ok := reader.ToBool(resolve(r.doc, dict.Get("ImageMask"))); ok && mask {
		// A stencil is one bit a pixel, so the bytes have to be samples. When
		// the filter chain stopped at an image format nothing here decodes,
		// they are not: they are still compressed, and drawing them paints
		// noise through the shape of nothing.
		//
		// 273 of the image masks in the 1 633 real forms carry an encoded
		// filter. 236 of them were faxes and nine were JBIG2, both of which
		// are decoded before this point. What is left is a format nothing
		// here reads, and the honest answer is not to draw it. That is the
		// rule the rest of this function already follows: "the image is not
		// drawn rather than drawn wrong".
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
		out = r.decodeJPEG(data, w, h, r.decodeInverts(dict))
	case "JPXDecode":
		out = r.decodeJPX(data, w, h)
	}
	// No arm ran, or the one that ran could not read its bytes: the image is
	// not drawn rather than drawn wrong. Every filter the reader hands back
	// unread has an arm above, so the first of those is a case this cannot
	// reach today and the check is here for the second.
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
func (r *renderer) decodeJPEG(data []byte, w, h int, inverted bool) *sampled {
	cw, ch := jpegSize(data)
	if !r.affordDecoded(cw, ch, w*h) {
		return nil
	}
	img, err := jpegDecode(data)
	if err != nil {
		return nil
	}
	img = uninvertAdobeCMYK(img, inverted)
	b := img.Bounds()
	if b.Dx() != w || b.Dy() != h {
		w, h = b.Dx(), b.Dy()
	}
	src := raster.FromImage(img)
	return &sampled{w: w, h: h, pix: src.Pix}
}

// decodeJPX reads a JPEG 2000 image, which is what a scanned page is stored in.
//
// Measured over a corpus of a thousand scanned documents: all 250 biodiversity
// scans carry one, 248 of the 250 medical ones do, and 144 of the 222 readable
// scanned books — and between them 655 pages have nothing on them at all
// besides such an image. Those pages came out blank.
//
// The size is taken from the picture rather than from the dictionary, as it is
// for JPEG: a codestream carries its own, and where the two disagree the one
// the pixels are actually in is the one that can be drawn.
func (r *renderer) decodeJPX(data []byte, w, h int) *sampled {
	cw, ch := jpxSize(data)
	if !r.affordDecoded(cw, ch, w*h) {
		return nil
	}
	img, err := jpxDecode(data)
	if err != nil || img == nil {
		return nil
	}
	if img.W != w || img.H != h {
		w, h = img.W, img.H
	}
	return &sampled{w: w, h: h, pix: img.Pix}
}

// affordDecoded reports whether a picture of cw by ch pixels may be made.
//
// A codec carries its own size and it need not be the one the dictionary
// declares, so the dictionary's is not enough to go on: image/jpeg makes the
// whole picture the moment it reaches the start of scan, so a 376-byte JPEG
// whose frame header claims 65 535 by 65 535 allocates four gigabytes and only
// then says the scan data is missing. Reading the header first costs nothing
// and allocates nothing, and it is the only place the question can be asked in
// time.
//
// charged is what the dictionary already paid for this picture, since [Images]
// charges the declared size before it gets here; a codestream claiming more
// than the dictionary pays the difference. [Page] keeps no picture and is not
// bounded that way, so its renderer spends nothing and only the ceiling on a
// single picture applies to it.
func (r *renderer) affordDecoded(cw, ch, charged int) bool {
	if cw <= 0 || ch <= 0 {
		// Nothing could be read from the header, so nothing will be made from
		// the body either: the decoder gives up before it allocates.
		return true
	}
	if cw > maxImagePixels || ch > maxImagePixels || cw*ch > maxImagePixels {
		return false
	}
	if !r.bounded {
		return true
	}
	extra := cw*ch - charged
	if extra <= 0 {
		return true
	}
	if extra > r.budget {
		r.refused = fmt.Errorf("%w: a picture whose codestream holds %d by %d pixels, with %d of the %d pixels left",
			ErrTooMuchToDecode, cw, ch, r.budget, maxImagesPixels)
		return false
	}
	r.budget -= extra
	return true
}

// jpegSize is how large a JPEG says it is, read from its header alone. It is
// zero when nothing can be read, which is a decoder's problem and not a
// budget's.
var jpegSize = func(data []byte) (int, int) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// jpxSize is the same question asked of a JPEG 2000 codestream.
var jpxSize = func(data []byte) (int, int) {
	cfg, err := jpeg2000.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// jpxDecode is a variable so a test can watch what happens when a decoder
// refuses what it is given.
var jpxDecode = func(data []byte) (*raster.Image, error) {
	img, err := jpeg2000.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return raster.FromImage(img), nil
}

// jpegDecode is a variable so a test can watch what happens when a decoder
// refuses what it is given.
var jpegDecode = func(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// uninvertAdobeCMYK turns a four-component JPEG's ink over.
//
// A CMYK JPEG written by an Adobe tool stores its ink inverted, and every PDF
// reader turns it back: poppler, mupdf and pdf.js all do. Go's image/jpeg
// targets JPEG files rather than PDFs and assumes the opposite — its own
// comment says the inversion "cancels out" — so a page that other readers show
// as a figure on white paper comes out of it almost solid black.
//
// # EXPERIMENT
//
// 35 of 8 300 corpus files carry a CMYK JPEG, 114 images between them, 95
// carrying the Adobe marker. Rendering all 35 both ways and comparing each
// against poppler at the same size settles which reading is right; the numbers
// are in the commit message. It is a narrow defect and a total one: the pages
// it hits are not slightly wrong, they are black.
func uninvertAdobeCMYK(img image.Image, decodeInverts bool) image.Image {
	cm, ok := img.(*image.CMYK)
	if !ok {
		return img
	}
	// A /Decode of [1 0 1 0 1 0 1 0] asks for the samples to be turned over,
	// and that is the second half of this: the two inversions cancel, which is
	// exactly what such a file means. Eleven DVLA forms in the corpus are
	// written that way and eleven are drawn right today only because both
	// halves were missing at once.
	if decodeInverts {
		return img
	}
	out := image.NewCMYK(cm.Bounds())
	for i, v := range cm.Pix {
		out.Pix[i] = 255 - v
	}
	return out
}

// decodeInverts says whether a /Decode array asks for every component to be
// read backwards, which for an image drawn from a JPEG is the only shape of
// /Decode the corpus contains.
func (r *renderer) decodeInverts(dict reader.Dict) bool {
	arr, ok := reader.ToArray(resolve(r.doc, dict.Get("Decode")))
	if !ok || len(arr) < 2 || len(arr)%2 != 0 {
		return false
	}
	for i := 0; i+1 < len(arr); i += 2 {
		lo, ok1 := reader.ToFloat(resolve(r.doc, arr[i]))
		hi, ok2 := reader.ToFloat(resolve(r.doc, arr[i+1]))
		if !ok1 || !ok2 || lo != 1 || hi != 0 {
			return false
		}
	}
	return true
}

// applyTransparency reads whichever of the two ways a PDF says which parts of
// an image are see-through: a soft mask of its own, or a range of colours to
// treat as absent.
// It reports whether the image may be drawn. A mask that is NAMED and cannot be
// READ means how much of the image shows is unknown, and an image drawn whole
// when most of it was meant to be invisible is worse than one not drawn: that
// is exactly how a scanned page goes wrong. Such a page is a low-resolution
// colour background with a high-resolution bitonal ink layer over it, and the
// ink layer is a dark rectangle masked by a JBIG2 stencil. Without the stencil
// it is a dark rectangle over the whole page.
func (r *renderer) applyTransparency(s *sampled, dict reader.Dict, resources reader.Dict) bool {
	if stream, ok := reader.ToStream(resolve(r.doc, dict.Get("SMask"))); ok {
		return r.applySoftMask(s, stream, resources)
	}
	maskEntry := resolve(r.doc, dict.Get("Mask"))
	if stream, ok := reader.ToStream(maskEntry); ok {
		return r.applyStencilMask(s, stream, resources)
	}
	return true
}

// applySoftMask reads a grey image whose levels say how much of each pixel
// shows.
func (r *renderer) applySoftMask(s *sampled, stream *reader.Stream, resources reader.Dict) bool {
	mask := r.decodeImage(stream.Dict, stream.Raw, resources)
	if mask == nil {
		return false
	}
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			m := mask.at(x*mask.w/s.w, y*mask.h/s.h)
			s.pix[(y*s.w+x)*4+3] = m.R
		}
	}
	return true
}

// applyStencilMask reads a one-bit image that says which parts of this one are
// painted.
//
// A mask sample of 0 means PAINT. The bit and the coverage run opposite ways,
// which is the whole difficulty: decodeImage returns a stencil whose alpha is
// set where the sample is 0, so alpha here already means "painted" and what has
// to be cleared is everything else. Reading the alpha as though it were the
// sample shows the exact complement of the picture — a scanned page whose text
// is the only part hidden.
//
// Asked which half of a two-colour page a mask of eight 0 bits and eight 1 bits
// paints, poppler answers the 0 half.
func (r *renderer) applyStencilMask(s *sampled, stream *reader.Stream, resources reader.Dict) bool {
	mask := r.decodeImage(stream.Dict, stream.Raw, resources)
	if mask == nil {
		return false
	}
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			m := mask.at(x*mask.w/s.w, y*mask.h/s.h)
			if m.A <= 127 {
				s.pix[(y*s.w+x)*4+3] = 0
			}
		}
	}
	return true
}

// intOr reads an integer, or gives a default.
func intOr(o reader.Object, def int64) int64 {
	if v, ok := reader.ToInt(o); ok {
		return v
	}
	return def
}
