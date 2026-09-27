package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	jpeg "github.com/go-images/jpeg"
	jpeg2000 "github.com/go-images/jpeg2000"
	"github.com/go-pdfkit/reader"
)

// A sampled image is what a PDF image XObject or an inline image comes down
// to: a grid of colours, some of which may be see-through.
type sampled struct {
	w, h int
	// pix is four bytes a pixel, straight alpha, the same shape as a raster
	// image so that drawing it is a matter of sampling -- unless pal is set,
	// and then it is ONE byte a pixel and pal says what each byte means.
	pix []uint8
	// pal turns one byte into a colour, and is set for a picture of a single
	// component of at most eight bits.
	//
	// That is not a rare shape: every sampled image in a corpus of 250 scanned
	// documents is of it, 2.05 billion pixels between them. In this form such a
	// picture costs a QUARTER of the memory, which is what lets the ceiling on
	// how large a picture may be become a ceiling on BYTES rather than on
	// pixels -- see maxImageBytes. A 400 dpi bilevel newspaper page is 83
	// megapixels and 83 MB here, where it was 333 MB and refused.
	pal []color.RGBA
}

// at reads one pixel.
func (s *sampled) at(x, y int) color.RGBA {
	if s.pal != nil {
		return s.pal[s.pix[y*s.w+x]]
	}
	i := (y*s.w + x) * 4
	return color.RGBA{R: s.pix[i], G: s.pix[i+1], B: s.pix[i+2], A: s.pix[i+3]}
}

// expand turns the one-byte form into the four-byte one.
//
// The one-byte form cannot be written to a pixel at a time: its bytes are
// indices into a palette every pixel shares, so changing one pixel's colour
// would change every pixel that names the same entry. The three callers that do
// write pixels -- a soft mask's alpha, a colour key, a stencil's fill -- call
// this first. None of them can be reached with a palette in place, because
// onePerPixel refuses the form for a picture that names a mask or is one; this
// is the floor under that, not a path the corpus takes.
func (s *sampled) expand() {
	if s.pal == nil {
		return
	}
	pix := make([]uint8, s.w*s.h*4)
	for i, v := range s.pix {
		c := s.pal[v]
		pix[i*4], pix[i*4+1], pix[i*4+2], pix[i*4+3] = c.R, c.G, c.B, c.A
	}
	s.pix, s.pal = pix, nil
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

// maxImageBytes bounds how much memory one picture may be decoded INTO, so a
// file cannot name a grid it would take the whole machine to hold.
//
// The bound used to be a pixel count, 64 << 20, with four bytes a pixel implied:
// the same 256 MB. Expressed in pixels it refused things it had no reason to.
// A 400 dpi bilevel newspaper scan is 7 779 by 10 699 -- 83 megapixels, past the
// old ceiling -- and in the one-byte form it holds in 83 MB, a third of what the
// ceiling allows. Five pages of the measured corpus came back BLANK for that
// reason, with a third to a half of their pixels wrong, and fast enough that a
// speed table read them as wins.
const maxImageBytes = 256 << 20

// maxImagePixels is the same bound for the paths whose cost is four bytes a
// pixel and is known to be: a JPEG or JPEG 2000 codestream, which decodes to
// colour whatever it holds.
const maxImagePixels = maxImageBytes / 4

// memoAndPack decides the two things samples() has to decide, and it is a
// function of its own because conflating them was a defect.
//
// memo says a table of 256 answers can replace a conversion at every pixel. It
// depends on the picture's SAMPLES and nothing else: one component of at most
// eight bits has at most 256 values.
//
// packed says the picture may additionally be HELD as one byte a pixel and that
// table. It depends on what will happen to the picture LATER -- nothing may write
// to its pixels, since a palette is shared between them.
//
// v0.49.0 made memo depend on packed. A single-component picture with a soft mask
// is refused the packed form, so it lost the memo too and went back to converting
// every pixel: two forms of the corpus went from 32 ms to 369 ms with
// byte-identical output. A proof of pixels cannot see that, and it took timing the
// whole corpus to find it.
func memoAndPack(n, bpc int, bounded, mayPack bool) (memo, packed bool) {
	memo = n == 1 && bpc <= 8
	return memo, memo && !bounded && mayPack
}

// mayPackOneByte says whether a picture MIGHT be held as one byte a pixel and a
// palette, asking only what is cheap to ask.
//
// It deliberately does NOT build the colour space. The first version of this
// predicate ended with
//
//	return r.colourSpace(dict.Get("ColorSpace"), resources, 0).components == 1
//
// which reads as a component count and is nothing of the kind: for an Indexed
// space over an ICC profile, constructing it parses the profile and builds the
// transform. Called twice per picture on top of the one samples() already does,
// that took two French forms from 32ms to 363ms -- an ELEVEN-FOLD regression with
// byte-identical output, which a corpus proof of pixels cannot see.
//
// So the cheap half is asked here, to bound the memory before any is spent, and
// the component count is read in samples() from the space it has already built.
//
// A picture that names a mask or IS one is refused the packed form: applying a
// mask writes pixels, and a palette is shared between them.
func (r *renderer) mayPackOneByte(dict reader.Dict) bool {
	bpc := int(intOr(resolve(r.doc, dict.Get("BitsPerComponent")), 8))
	if bpc > 8 {
		return false
	}
	if b, ok := reader.ToBool(resolve(r.doc, dict.Get("ImageMask"))); ok && b {
		return false
	}
	// Asked the way applyTransparency asks it -- resolve, then try to convert.
	// Dict.Get returns Null{} for a key that is not there, NEVER nil, so
	// `dict.Get("SMask") != nil` is true of every dictionary in every file.
	if _, ok := reader.ToStream(resolve(r.doc, dict.Get("SMask"))); ok {
		return false
	}
	if _, ok := reader.ToStream(resolve(r.doc, dict.Get("Mask"))); ok {
		return false
	}
	return true
}

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
	// int64 for the same reason as in affordDecoded: these two numbers come
	// out of the file, and their product does not fit a 32-bit int.
	//
	// The bound is on BYTES, and how many bytes a pixel costs is asked of the
	// dictionary before a single one is spent.
	// r.bounded is true exactly on the Images path, which hands pictures OUT
	// through raster.Image and so expands them to four bytes a pixel. Budget
	// what that path will really spend, or the ceiling would promise 256 MB and
	// the expansion would take 333.
	// mayPackOneByte does not know how many components there are, so this is a
	// LOWER bound on the cost: a picture it admits may still turn out to need
	// four bytes a pixel, and samples() checks the real cost against the same
	// ceiling once it knows. Bounding loosely here and exactly there is what
	// keeps the expensive question out of the guard.
	bpp := int64(4)
	if !r.bounded && r.mayPackOneByte(dict) {
		bpp = 1
	}
	if w <= 0 || h <= 0 || int64(w)*int64(h)*bpp > maxImageBytes {
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
		out = r.decodeJPEG(dict, data, w, h, resources)
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
	rowBits := w * n * bpc
	rowBytes := (rowBits + 7) / 8
	comps := make([]float64, n)
	// The real cost is known now: n components at bpc bits become either one
	// byte a pixel and a palette, or four. decodeBase bounded this loosely
	// without the colour space; this is the same ceiling, exactly.
	// TWO DECISIONS, and conflating them cost eleven-fold on two forms of this
	// corpus with byte-identical output.
	//
	// The first is whether a table of 256 answers can replace a conversion at
	// every pixel: that needs only one component of at most eight bits, and it
	// is worth having whatever else is true of the picture. Every sampled image
	// in one corpus of 250 scanned documents is of this shape, 2.05 BILLION
	// pixels between them.
	//
	// The second is whether the picture may then be HELD as one byte a pixel and
	// that table. That needs more -- nothing may write to its pixels later, since
	// a palette is shared between them -- and gating the TABLE on it was the
	// mistake: a single-component picture with a soft mask lost its memo and went
	// back to converting 3.93 million pixels one at a time.
	memo, packed := memoAndPack(n, bpc, r.bounded, r.mayPackOneByte(dict))
	cost := int64(4)
	if packed {
		cost = 1
	}
	if int64(w)*int64(h)*cost > maxImageBytes {
		return nil
	}
	if memo {
		// The table is filled by the same decode and the same convert the loop
		// below would have called, so it holds the same answers: this is a memo,
		// not a second way of computing them.
		//
		// Alpha is 255 because that is what the four-byte loop below writes,
		// whatever convert returned.
		pal := make([]color.RGBA, 256)
		for raw := range 1 << bpc {
			comps[0] = decode(0, uint32(raw), bpc)
			c := sp.convert(comps)
			pal[raw] = color.RGBA{R: c.R, G: c.G, B: c.B, A: 255}
		}
		if packed {
			out := &sampled{w: w, h: h, pix: make([]uint8, w*h), pal: pal}
			for y := 0; y < h; y++ {
				rowStart := y * rowBytes
				for x := 0; x < w; x++ {
					out.pix[y*w+x] = uint8(sampleAt(data, rowStart, x*bpc, bpc))
				}
			}
			return out
		}
		out := &sampled{w: w, h: h, pix: make([]uint8, w*h*4)}
		for y := 0; y < h; y++ {
			rowStart := y * rowBytes
			for x := 0; x < w; x++ {
				col := pal[sampleAt(data, rowStart, x*bpc, bpc)]
				i := (y*w + x) * 4
				out.pix[i], out.pix[i+1], out.pix[i+2], out.pix[i+3] = col.R, col.G, col.B, 255
			}
		}
		return out
	}
	out := &sampled{w: w, h: h, pix: make([]uint8, w*h*4)}
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
		if sp.labRange != nil {
			// Lab is the one space whose default decode is not [0 1] per
			// component: lightness runs to 100 and the two opponent axes over
			// the space's own /Range.
			// Three components, and the caller only ever asks for one of
			// them: decodeArray is called with the space's own count.
			lo := [3]float64{0, sp.labRange[0], sp.labRange[2]}
			hi := [3]float64{100, sp.labRange[1], sp.labRange[3]}
			return func(c int, raw uint32, _ int) float64 {
				return lo[c] + float64(raw)*(hi[c]-lo[c])/maxValue
			}
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
func (r *renderer) decodeJPEG(dict reader.Dict, data []byte, w, h int, resources reader.Dict) *sampled {
	cw, ch := jpegSize(data)
	if !r.affordDecoded(cw, ch, w*h) {
		return nil
	}
	img, err := jpegDecode(data)
	if err != nil {
		return nil
	}
	img = uninvertAdobeCMYK(img, r.decodeInverts(dict))
	b := img.Bounds()
	if b.Dx() != w || b.Dy() != h {
		w, h = b.Dx(), b.Dy()
	}
	if out := r.jpegThroughSpace(dict, img, resources, w, h); out != nil {
		return out
	}
	src := jpegPixels(img)
	return &sampled{w: w, h: h, pix: src.Pix}
}

// jpegThroughSpace puts a JPEG's own samples through the colour space the
// image dictionary names, and reports nil when the codec's output is already
// the answer.
//
// A JPEG carries SAMPLES, not colours. For DeviceRGB, CalRGB, ICCBased and
// DeviceGray that distinction costs nothing: the numbers a decoder hands back
// are the numbers those spaces read, and image/jpeg has already done the
// YCbCr transform every reader does. For a space that TRANSFORMS its samples
// it costs the whole picture.
//
// A Separation's sample is an amount of INK. A tint of nothing is no ink,
// which is paper — white. Read as a level of grey, nothing is black, so such
// an image comes out as its own negative. Three French tax forms carry a
// PANTONE 293 U logo over an ICCBased CMYK alternate and were drawn inverted;
// a DVLA form carries a DeviceN "Black" over DeviceCMYK whose every pixel was
// 255 levels from what poppler extracts -- solid black against solid white,
// the largest disagreement in the corpus.
//
// This is what poppler does and it is not an interpretation of it: DCTStream
// hands GfxImageColorMap the component samples, and the colour map is what
// turns them into colour. The shortcut here was correct for every space but
// the ones that are not device spaces.
//
// Only ONE-component JPEGs are put through, which is every such picture in the
// corpus: 5 of the 2598 forms, and none of the 682 scans. A DeviceN of three
// or four tints would need the samples BEFORE the YCbCr transform image/jpeg
// applies to a three-component file, and taking its RGB output as tints would
// be a second wrong answer rather than a fix. Left undone deliberately, and it
// is a defect the day a file needs it.
func (r *renderer) jpegThroughSpace(dict reader.Dict, img image.Image, resources reader.Dict, w, h int) *sampled {
	if cm, ok := img.(*image.CMYK); ok {
		// A four-component JPEG's samples are ink, and raster.FromImage turns
		// them into colour with the standard library's naive formula rather
		// than with the one the rest of this package uses. Eight DVLA forms
		// carry a YCCK scan of a whole page and every one of them differed
		// from poppler on 99% of its pixels for that reason alone.
		//
		// Only the CONVERSION changes here. Which samples to convert is
		// already settled above: uninvertAdobeCMYK has read the /Decode array
		// and turned the ink over or left it, and reading it a second time
		// would undo that.
		return cmykPicture(cm, w, h)
	}
	sp := r.colourSpace(dict.Get("ColorSpace"), resources, 0)
	if passesSamplesThrough(sp) {
		return nil
	}
	// The same reading `samples` gives packed samples: a /Decode array still
	// applies, and an Indexed space's samples are row numbers rather than
	// fractions.
	decode := r.decodeArray(dict, sp, 8)
	out := &sampled{w: w, h: h, pix: make([]uint8, w*h*4)}
	comps := make([]float64, sp.components)
	switch {
	case sp.components == 1:
		g, ok := img.(*image.Gray)
		if !ok {
			return nil
		}
		// An eight-bit sample has 256 possible values, so 256 answers are ALL the
		// answers: this memo is exact rather than an approximation, because both
		// decode and sp.convert are functions of the sample alone.
		//
		// Without it this loop asked a colour space to convert every pixel one at
		// a time. On a DVLA form carrying one 2480x3508 ICCBased grey scan --
		// v55-5, 8.7 MILLION pixels -- that was 72% of the page: a math.Pow per
		// sample through an ICC grey TRC, 535 ms against poppler's 159. The same
		// memo exists in `samples` for the same reason, and `memoAndPack` states
		// the condition (one component, eight bits or fewer); this path had simply
		// never been given it.
		var memo [256][4]uint8
		for v := 0; v < 256; v++ {
			comps[0] = decode(0, uint32(v), 8)
			col := sp.convert(comps)
			memo[v] = [4]uint8{col.R, col.G, col.B, 255}
		}
		b := img.Bounds()
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := memo[g.GrayAt(b.Min.X+x, b.Min.Y+y).Y]
				i := (y*w + x) * 4
				out.pix[i], out.pix[i+1], out.pix[i+2], out.pix[i+3] = c[0], c[1], c[2], c[3]
			}
		}
	case sp.components == 3:
		// A three-component JPEG's samples are the space's three components,
		// and the decoder has already turned the stored YCbCr back into them:
		// that conversion is the JPEG's own, not the colour space's, and it
		// runs first. What is left is to ask the space what the three numbers
		// mean, which for a calibrated space is a gamma, a matrix and an
		// adaptation rather than nothing at all.
		src := jpegPixels(img)
		// Cached on the sample triple: 16.7 million triples are not a memo, but a
		// picture is not 16.7 million colours. See tripleCache for what was counted
		// and for the two cheaper caches that were measured and are too weak.
		//
		// The table belongs to the renderer and this picture takes a generation of
		// it, because the answer depends on the colour space and the /Decode array
		// as well as on the triple, and two pictures on one page may differ in
		// both.
		cache := &r.triples
		cache.nextImage()
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				sr, sg, sb := src.Pix[i], src.Pix[i+1], src.Pix[i+2]
				cr, cg, cb, ok, at := cache.lookup(sr, sg, sb)
				if !ok {
					comps[0] = decode(0, uint32(sr), 8)
					comps[1] = decode(1, uint32(sg), 8)
					comps[2] = decode(2, uint32(sb), 8)
					col := sp.convert(comps)
					cr, cg, cb = col.R, col.G, col.B
					cache.store(at, sr, sg, sb, cr, cg, cb)
				}
				out.pix[i], out.pix[i+1], out.pix[i+2], out.pix[i+3] = cr, cg, cb, 255
			}
		}
	default:
		return nil
	}
	return out
}

// passesSamplesThrough reports a space that reads a picture's decoded samples
// as colour directly, so putting them through it would give back what went in.
//
// The four device spaces are package-level singletons and every path that
// yields one yields the same pointer -- byComponents for an ICCBased profile
// this package does not interpret, deviceSpace for a bare name -- so identity
// is the whole test. A calibrated space is NOT one of them even though it
// carries the same number of components under a similar name, which is the
// distinction this function exists to draw.
func passesSamplesThrough(sp *space) bool {
	return sp == deviceGray || sp == deviceRGB || sp == deviceCMYK || sp == patternSpace
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
//
// A FOUR-COMPONENT codestream used to be drawn wrong here, and the note that
// said so called it unfixable. It said the decoder's public API hands back an
// image.RGBA and nothing else, so the black plate was gone before render saw
// it, and that closing the gap meant components out of a third-party module.
//
// That was true of the module we depended on and false as a conclusion: the
// module could be forked. github.com/go-images/jpeg2000 is that fork, and it
// reads a picture whose own JP2 header declares the enumerated colour space
// CMYK as an image.CMYK. gh-pdfbox/JPXTestCMYK.pdf, 1377x443, went from 255
// levels from poppler on every pixel to at most 2 over 2 440 044 samples.
//
// What is left here is the second half of the same lesson the CMYK JPEG path
// taught: a picture that arrives as ink must reach colour through the printing
// primaries, not through raster.FromImage's (1-c)(1-k).
func (r *renderer) decodeJPX(data []byte, w, h int) *sampled {
	cw, ch := jpxSize(data)
	if !r.affordDecoded(cw, ch, w*h) {
		return nil
	}
	img, err := jpxDecode(data)
	if err != nil || img == nil {
		return nil
	}
	b := img.Bounds()
	if b.Dx() != w || b.Dy() != h {
		w, h = b.Dx(), b.Dy()
	}
	// A JPEG 2000 picture whose own JP2 header declares CMYK comes back as
	// ink, and ink reaches colour through the printing primaries rather than
	// through (1-c)(1-k). It is the same distinction the CMYK JPEG path drew:
	// that one went through raster.FromImage and answered differently from
	// every other CMYK picture in the package.
	if cm, ok := img.(*image.CMYK); ok {
		return cmykPicture(cm, w, h)
	}
	if s := adopted(img, w, h); s != nil {
		return s
	}
	return &sampled{w: w, h: h, pix: raster.FromImage(img).Pix}
}

// adopted takes an *image.RGBA's own bytes instead of converting them, and
// reports nil when that would not be the same picture.
//
// raster.FromImage converts rather than copies because image.RGBA is
// PREMULTIPLIED and a raster.Image is straight -- but at an alpha of 255 the two
// are the same bytes, and a JPEG 2000 codestream carries no alpha, so every pixel
// the decoder writes is opaque. Then the conversion is a full pass over the image
// and a second allocation the size of it: 0.11s and 12 MB on a scanned page of
// this corpus, and 494 MB on its largest.
//
// Opaque() is what makes this safe rather than assumed. It reads one byte in four
// and stops at the first pixel that is not opaque, so the cost of being wrong is a
// scan, not a wrong picture. The stride and origin are checked because a raster
// image is densely packed from (0,0) and a sub-image of a larger one is not.
func adopted(img image.Image, w, h int) *sampled {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return nil
	}
	b := rgba.Bounds()
	if b.Min != (image.Point{}) || rgba.Stride != w*4 || b.Dx() != w || b.Dy() != h {
		return nil
	}
	if len(rgba.Pix) < w*h*4 || !rgba.Opaque() {
		return nil
	}
	return &sampled{w: w, h: h, pix: rgba.Pix[:w*h*4]}
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
	// In int64, because int is 32 bits on a 32-bit build and this product is
	// two numbers out of a file: a codestream declaring 65 535 by 65 535 makes
	// 4 294 836 225, which wraps to MINUS 131 071 in an int32 and walks
	// straight through a ceiling written as `>`. Its sibling afford already
	// said why, one file over. Either side alone is still refused first, so a
	// width past the ceiling never reaches the multiplication at all.
	if cw > maxImagePixels || ch > maxImagePixels || int64(cw)*int64(ch) > maxImagePixels {
		return false
	}
	if !r.bounded {
		return true
	}
	extra := int64(cw)*int64(ch) - int64(charged)
	if extra <= 0 {
		return true
	}
	if extra > int64(r.budget) {
		r.refused = fmt.Errorf("%w: a picture whose codestream holds %d by %d pixels, with %d of the %d pixels left",
			ErrTooMuchToDecode, cw, ch, r.budget, maxImagesPixels)
		return false
	}
	// extra is at most the budget here, and the budget is at most
	// maxImagesPixels, so this fits an int on every build.
	r.budget -= int(extra)
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
//
// It hands back the decoded image rather than a raster, because what the
// image IS decides how its numbers become colour: raster.FromImage reads an
// *image.CMYK with the standard library's naive formula, which is not the one
// the rest of this package uses. decodeJPX asks.
var jpxDecode = func(data []byte) (image.Image, error) {
	return jpeg2000.Decode(bytes.NewReader(data))
}

// jpegDecode is a variable so a test can watch what happens when a decoder
// refuses what it is given.
//
// It names the decoder rather than going through image.Decode's registry,
// because which decoder runs is the point. go-images/jpeg is Go's own
// image/jpeg with one change: a FOUR-component picture's chroma is upsampled
// the way libjpeg does rather than by repeating each sample. The standard
// library merges those four planes itself and hands back an *image.CMYK, so a
// caller cannot put that right afterwards -- the planes are gone. On the
// corpus's 258x258 YCCK picture it is worth 36 levels to 2 on the cyan plate.
//
// A /DCTDecode stream is a JPEG by definition, so there is nothing for a
// registry to sniff.
var jpegDecode = func(data []byte) (image.Image, error) {
	return jpeg.Decode(bytes.NewReader(data))
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
	s.expand()
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
	s.expand()
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

// cmykPicture converts a four-component picture the way every other CMYK in
// this package is converted, which is through the printing primaries.
func cmykPicture(cm *image.CMYK, w, h int) *sampled {
	out := &sampled{w: w, h: h, pix: make([]uint8, w*h*4)}
	b := cm.Bounds()
	v := make([]float64, 4)
	for y := 0; y < h; y++ {
		row := cm.Pix[cm.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := 0; x < w; x++ {
			for c := 0; c < 4; c++ {
				v[c] = float64(row[x*4+c]) / 255
			}
			col := cmykToRGBA(v)
			i := (y*w + x) * 4
			out.pix[i], out.pix[i+1], out.pix[i+2], out.pix[i+3] = col.R, col.G, col.B, 255
		}
	}
	return out
}
