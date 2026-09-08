// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"github.com/go-pdfkit/reader"
)

// greyJPEGPage draws one flat grey JPEG of the given level, in the given
// colour space, over a whole 8 by 8 page.
func greyJPEGPage(t *testing.T, level uint8, space func(w *reader.Writer) reader.Object, extra reader.Dict) *reader.Document {
	t.Helper()
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range src.Pix {
		src.Pix[i] = level
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"ColorSpace": space(w), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode"),
	}
	for k, v := range extra {
		dict[k] = v
	}
	img := w.Add(&reader.Stream{Dict: dict, Raw: buf.Bytes()})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 8, 8),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 8 0 0 8 0 0 cm /I Do Q")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestASeparationJPEGIsInkAndNotGrey is the defect this was written for.
//
// A Separation's sample is an amount of INK. A tint of nothing is no ink,
// which is paper: white. Read as a level of grey, nothing is black -- so the
// picture comes out as its own negative. Three French tax forms carry a
// PANTONE 293 U logo drawn that way, and a DVLA form's DeviceN "Black" was
// 255 levels from what poppler extracts on every pixel of it.
func TestASeparationJPEGIsInkAndNotGrey(t *testing.T) {
	d := greyJPEGPage(t, 0, func(w *reader.Writer) reader.Object {
		// No tint transform, so the fallback stands: a tint of one is full
		// ink and a tint of nothing is none.
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray")}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Sample 0 is a tint of nothing, which is no ink at all.
	if !isWhite(pic, 4, 4) {
		t.Errorf("a JPEG carrying no ink drew %s, want paper", pixel(pic, 4, 4))
	}
}

// TestASeparationJPEGWithFullInkIsDark is the other end of the same rule, so
// that a change making everything white would not pass.
func TestASeparationJPEGWithFullInkIsDark(t *testing.T) {
	d := greyJPEGPage(t, 255, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray")}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(pic, 4, 4) {
		t.Errorf("a JPEG carrying full ink drew %s, want ink", pixel(pic, 4, 4))
	}
}

// TestADecodeArrayStillAppliesToASeparationJPEG: the samples reach the space
// through the same reading `samples` gives them, so /Decode is not lost on the
// way. A tint written [1 0] means the file stored its ink the other way up.
func TestADecodeArrayStillAppliesToASeparationJPEG(t *testing.T) {
	d := greyJPEGPage(t, 0, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray")}
	}, reader.Dict{"Decode": nums(1, 0)})
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Sample 0 through [1 0] is a tint of one: full ink.
	if !isBlack(pic, 4, 4) {
		t.Errorf("a turned-over tint drew %s, want ink", pixel(pic, 4, 4))
	}
}

// TestAnIndexedJPEGReadsItsPalette: an indexed image's samples are row
// numbers, and the colour is whatever the table holds. Every entry here is the
// same blue, so a decoder that hands back the sample as a level of grey cannot
// pass by accident.
func TestAnIndexedJPEGReadsItsPalette(t *testing.T) {
	table := make([]byte, 256*3)
	for i := 0; i < 256; i++ {
		table[i*3], table[i*3+1], table[i*3+2] = 0, 0, 255
	}
	d := greyJPEGPage(t, 128, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Indexed"), reader.Name("DeviceRGB"),
			reader.Integer(255), w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: table})}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := pic.At(4, 4)
	r, g, b, _ := c.RGBA()
	if r>>8 > 8 || g>>8 > 8 || b>>8 < 240 {
		t.Errorf("an indexed JPEG drew %s, want the palette's blue", pixel(pic, 4, 4))
	}
}

// TestAGreyJPEGInAGreySpaceStillDrawsAsGrey holds the three spellings of "this
// is a level of grey" to the answer they had before.
//
// It does NOT test that the switch excludes them: putting DeviceGray through
// the space machinery gives the same pixels, which was checked by making the
// switch accept it and watching this pass. For a grey device space the two
// routes agree by construction, so the switch is about saying what the code
// means and what it costs, not about a different answer. What it guards is the
// regression: a change that put every JPEG through a space would break these.
func TestAGreyJPEGInAGreySpaceStillDrawsAsGrey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		space func(w *reader.Writer) reader.Object
	}{
		{"DeviceGray", func(w *reader.Writer) reader.Object { return reader.Name("DeviceGray") }},
		{"CalGray", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Name("CalGray"), reader.Dict{}}
		}},
		{"ICCBased N=1", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Name("ICCBased"), w.Add(&reader.Stream{
				Dict: reader.Dict{"N": reader.Integer(1)}, Raw: []byte{}})}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := greyJPEGPage(t, 0, tc.space, nil)
			pic, err := Page(d, 1, Options{Scale: 1})
			if err != nil {
				t.Fatal(err)
			}
			if !isBlack(pic, 4, 4) {
				t.Errorf("a black grey JPEG drew %s", pixel(pic, 4, 4))
			}
		})
	}
}

// TestADeviceNOfManyTintsOverAGreyJPEGIsLeftAlone pins the limit rather than
// hiding it.
//
// A DeviceN naming four tints wants four numbers per pixel, and a grey JPEG
// carries one. Nothing here can invent the other three: feeding the one sample
// to all four, or to the first, would be a guess with a colour attached. The
// picture is drawn as the codec produced it, which is what happened before
// this existed, and the file gets no worse for having been looked at.
func TestADeviceNOfManyTintsOverAGreyJPEGIsLeftAlone(t *testing.T) {
	d := greyJPEGPage(t, 0, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("DeviceN"),
			reader.Array{reader.Name("C"), reader.Name("M"), reader.Name("Y"), reader.Name("K")},
			reader.Name("DeviceCMYK")}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(pic, 4, 4) {
		t.Errorf("drew %s, want the codec's own output", pixel(pic, 4, 4))
	}
}
