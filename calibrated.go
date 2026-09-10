package render

import (
	gfxcolor "github.com/go-gfx/gfx/color"
	"github.com/go-pdfkit/reader"
	"image/color"
)

// This file reads the three CIE-based spaces: CalGray, CalRGB and Lab. Each
// says what colour its numbers name by giving a white point and a rule for
// reaching CIE XYZ, so none of them is its device namesake and none can be
// read by passing its numbers through.
//
// Lab is the third and is here too. It was left out at first on the strength
// of a misreading: GfxLabColorSpace::getXYZ does not multiply by the white
// point where ISO 32000-2 8.6.5.4 says to, which looked like poppler
// disagreeing with the format. It does not -- ::getRGB multiplies immediately
// after calling it. A four-pixel Lab document run through pdfimages settles
// it: the specification's formula matches 4 of 4 pixels within one level, and
// the no-white-point formula is 13 levels out on a neutral mid tone.
//
// The arithmetic lives in gfx/color; what is here is reading a dictionary and
// carrying its defaults.

// calDict returns the dictionary an array-form space carries as its second
// element, which is where all three keep their parameters.
func (r *renderer) calDict(arr reader.Array) reader.Dict {
	if len(arr) < 2 {
		return nil
	}
	d, _ := reader.ToDict(resolve(r.doc, arr[1]))
	return d
}

// calFloats reads a numeric array of exactly n entries, reporting whether it
// was there and whole. A partial one is not used: a white point missing its
// Z is not a white point.
func (r *renderer) calFloats(d reader.Dict, key reader.Name, n int) ([]float64, bool) {
	arr, ok := reader.ToArray(resolve(r.doc, d.Get(key)))
	if !ok || len(arr) != n {
		return nil, false
	}
	out := make([]float64, n)
	for i := range out {
		v, ok := reader.ToFloat(resolve(r.doc, arr[i]))
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// calWhitePoint reads /WhitePoint, which all three spaces require. A file that
// omits it has said nothing about its colours, so the space falls back to its
// device namesake rather than to an invented illuminant.
func (r *renderer) calWhitePoint(d reader.Dict) (gfxcolor.WhitePoint, bool) {
	v, ok := r.calFloats(d, "WhitePoint", 3)
	if !ok || v[1] <= 0 {
		return gfxcolor.WhitePoint{}, false
	}
	return gfxcolor.WhitePoint{X: v[0], Y: v[1], Z: v[2]}, true
}

// calGraySpace reads a CalGray space: a white point and the gamma its single
// component is encoded by.
func (r *renderer) calGraySpace(arr reader.Array) *space {
	d := r.calDict(arr)
	white, ok := r.calWhitePoint(d)
	if !ok {
		return deviceGray
	}
	s := gfxcolor.NewCalGray(white)
	if g, ok := reader.ToFloat(resolve(r.doc, d.Get("Gamma"))); ok && g > 0 {
		s.Gamma = g
	}
	return &space{name: "CalGray", components: 1, convert: func(v []float64) color.RGBA {
		red, green, blue := gfxcolor.CalGrayToSRGB(s, at(v, 0))
		return color.RGBA{R: byteOf(red), G: byteOf(green), B: byteOf(blue), A: 255}
	}}
}

// calRGBSpace reads a CalRGB space: a white point, three gammas and the matrix
// carrying the decoded components to CIE XYZ.
func (r *renderer) calRGBSpace(arr reader.Array) *space {
	d := r.calDict(arr)
	white, ok := r.calWhitePoint(d)
	if !ok {
		return deviceRGB
	}
	s := gfxcolor.NewCalRGB(white)
	if g, ok := r.calFloats(d, "Gamma", 3); ok && g[0] > 0 && g[1] > 0 && g[2] > 0 {
		s.Gamma = [3]float64{g[0], g[1], g[2]}
	}
	if m, ok := r.calFloats(d, "Matrix", 9); ok {
		copy(s.Matrix[:], m)
	}
	return &space{name: "CalRGB", components: 3, convert: func(v []float64) color.RGBA {
		red, green, blue := gfxcolor.CalRGBToSRGB(s, at(v, 0), at(v, 1), at(v, 2))
		return color.RGBA{R: byteOf(red), G: byteOf(green), B: byteOf(blue), A: 255}
	}}
}

// labRangeDefault is the default /Range: the two opponent axes run from -100
// to 100 unless the space narrows them. Lightness is always 0 to 100 and is
// not part of /Range.
var labRangeDefault = [4]float64{-100, 100, -100, 100}

// labSpace reads a Lab space: a white point and the range of its two opponent
// axes.
//
// The range is carried on the space because an image decodes against it. Lab
// is the one space in the format whose default /Decode is not [0 1] per
// component: it is [0 100 amin amax bmin bmax], so an image that gives no
// /Decode of its own would otherwise have its lightness read as a hundredth of
// what it says.
func (r *renderer) labSpace(arr reader.Array) *space {
	d := r.calDict(arr)
	white, ok := r.calWhitePoint(d)
	if !ok {
		// Lab has no device namesake to fall back to, so unlike CalGray and
		// CalRGB there is nothing to decline into. (1, 1, 1) is the equal-
		// energy point, which makes the white-point multiplication the
		// identity -- the most conservative reading of a file that said
		// nothing -- and it is also what GfxLabColorSpace's constructor uses
		// when /WhitePoint is missing, so a malformed file is read the same
		// way by both.
		white = gfxcolor.WhitePoint{X: 1, Y: 1, Z: 1}
	}
	rng := labRangeDefault
	if v, ok := r.calFloats(d, "Range", 4); ok && v[0] <= v[1] && v[2] <= v[3] {
		copy(rng[:], v)
	}
	return &space{name: "Lab", components: 3, labRange: &rng, convert: func(v []float64) color.RGBA {
		red, green, blue := gfxcolor.LabToSRGBWP(
			gfxcolor.Lab{L: at(v, 0), A: at(v, 1), B: at(v, 2)}, white)
		return color.RGBA{R: byteOf(red), G: byteOf(green), B: byteOf(blue), A: 255}
	}}
}
