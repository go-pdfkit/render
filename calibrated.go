package render

import (
	gfxcolor "github.com/go-gfx/gfx/color"
	"github.com/go-pdfkit/reader"
	"image/color"
)

// This file reads the two calibrated spaces: CalGray and CalRGB. Each
// says what colour its numbers name by giving a white point and a rule for
// reaching CIE XYZ, so neither is its device namesake and neither can be read
// by passing its numbers through.
//
// Lab is the third CIE space and is NOT here. poppler's GfxLabColorSpace does
// not multiply by the white point where ISO 32000-2 8.6.5.4 says to
// (X = Xw*g(M)), so a correct Lab and the judge this repository measures
// against would disagree for a reason that is not ours. That wants an
// experiment of its own before either is changed.
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
