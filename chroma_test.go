// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"bytes"
	"image"
	imgcolor "image/color"
	"image/jpeg"
	"testing"

	"github.com/go-gfx/gfx/raster"
)

// ycbcr is a chroma plane written into a picture of w by h pixels, so a test
// can say what one plane holds without saying anything about the other two.
func ycbcr(t *testing.T, w, h int, r image.YCbCrSubsampleRatio, cb [][]uint8) *image.YCbCr {
	t.Helper()
	im := image.NewYCbCr(image.Rect(0, 0, w, h), r)
	for y, row := range cb {
		copy(im.Cb[y*im.CStride:], row)
		copy(im.Cr[y*im.CStride:], row)
	}
	return im
}

// TestChromaIsPutBackTheWayLibjpegPutsItBack pins the reconstruction against
// libjpeg's, one sampling at a time.
//
// The expected rows are not this code's output written down. They are
// libjpeg's expressions evaluated on these inputs: h2v1_fancy_upsample,
// h2v2_fancy_upsample and h1v2_fancy_upsample from jdsample.c, including the
// asymmetric biases — 1 for the upper row of a pair and 2 for the lower — that
// keep a pair's rounding from drifting one way. The whole point of this
// function is to agree with a decoder written in another language, so a test
// that only agreed with itself would assert nothing.
func TestChromaIsPutBackTheWayLibjpegPutsItBack(t *testing.T) {
	for _, c := range []struct {
		name  string
		ratio image.YCbCrSubsampleRatio
		plane [][]uint8
		want  [][]uint8
	}{{
		name:  "4:2:0, interpolated both ways",
		ratio: image.YCbCrSubsampleRatio420,
		plane: [][]uint8{{0, 60, 120}, {180, 240, 255}},
		want: [][]uint8{
			{0, 15, 45, 75, 105, 120},
			{45, 60, 90, 117, 142, 154},
			{135, 150, 180, 202, 215, 221},
			{180, 195, 225, 244, 251, 255},
		},
	}, {
		name:  "4:2:2, interpolated across only",
		ratio: image.YCbCrSubsampleRatio422,
		plane: [][]uint8{{0, 60, 120}, {180, 240, 255}, {10, 20, 30}, {200, 100, 50}},
		want: [][]uint8{
			{0, 15, 45, 75, 105, 120},
			{180, 195, 225, 244, 251, 255},
			{10, 13, 17, 23, 27, 30},
			{200, 175, 125, 88, 62, 50},
		},
	}, {
		name:  "4:4:0, interpolated down only",
		ratio: image.YCbCrSubsampleRatio440,
		plane: [][]uint8{{0, 60, 120, 180, 240, 255}, {255, 240, 180, 120, 60, 0}},
		want: [][]uint8{
			{0, 60, 120, 180, 240, 255},
			{64, 105, 135, 165, 195, 191},
			{191, 195, 165, 135, 105, 64},
			{255, 240, 180, 120, 60, 0},
		},
	}} {
		t.Run(c.name, func(t *testing.T) {
			im := ycbcr(t, 6, 4, c.ratio, c.plane)
			hf, vf := chromaFactors(c.ratio)
			rows := chromaRows(im.Cb, im, (6+hf-1)/hf, (4+vf-1)/vf, hf, vf)
			for y, row := range c.want {
				got := rows.at(y)
				for x, want := range row {
					if got[x] != want {
						t.Errorf("chroma at %d,%d is %d, libjpeg makes it %d",
							x, y, got[x], want)
					}
				}
			}
		})
	}
}

// TestChromaIsLeftAloneWhereLibjpegLeavesItAlone checks the pictures this must
// not touch: the ones libjpeg itself reconstructs by replication, where
// interpolating would make us the odd one out in the other direction.
func TestChromaIsLeftAloneWhereLibjpegLeavesItAlone(t *testing.T) {
	grey := image.NewGray(image.Rect(0, 0, 4, 4))
	grey.Set(1, 1, imgcolor.Gray{200})
	for _, c := range []struct {
		name string
		img  image.Image
	}{
		{"no chroma to put back at all", grey},
		{"4:4:4 is already full size",
			ycbcr(t, 6, 4, image.YCbCrSubsampleRatio444, [][]uint8{{1, 2, 3, 4, 5, 6}})},
		{"4:1:1 has no fancy method in libjpeg",
			ycbcr(t, 8, 4, image.YCbCrSubsampleRatio411, [][]uint8{{1, 2}})},
		{"4:1:0 has no fancy method in libjpeg",
			ycbcr(t, 8, 4, image.YCbCrSubsampleRatio410, [][]uint8{{1, 2}})},
		{"a chroma plane two samples wide is below libjpeg's own threshold",
			ycbcr(t, 4, 4, image.YCbCrSubsampleRatio420, [][]uint8{{7, 9}})},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := raster.FromImage(c.img)
			got := jpegPixels(c.img)
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Error("the picture was reconstructed where libjpeg replicates")
			}
		})
	}
}

// TestASubsampledJPEGComesOutInterpolated is the whole path: a JPEG with a
// hard colour edge, encoded 4:2:0, drawn through the renderer.
//
// Under replication the two pixels either side of the edge are the two source
// colours exactly and the pair inside one chroma block is identical; under
// libjpeg's filter the edge is graded, so the assertion is that the two
// columns differ. That is the difference the corpus measures as 16 to 62
// levels, seen in the small.
func TestASubsampledJPEGComesOutInterpolated(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if x < 8 {
				src.Set(x, y, imgcolor.RGBA{255, 0, 0, 255})
			} else {
				src.Set(x, y, imgcolor.RGBA{0, 0, 255, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := img.(*image.YCbCr); !ok {
		t.Fatalf("the encoder stopped subsampling: %T", img)
	}
	got, plain := jpegPixels(img), raster.FromImage(img)
	if bytes.Equal(got.Pix, plain.Pix) {
		t.Fatal("a 4:2:0 JPEG came out exactly as replication would have made it")
	}
	// The pair of pixels inside one chroma block, at the edge.
	a, b := got.Pix[(8*16+6)*4:], got.Pix[(8*16+7)*4:]
	if a[0] == b[0] && a[2] == b[2] {
		t.Error("the two pixels of one chroma block are still identical")
	}
}
