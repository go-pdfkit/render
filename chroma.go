// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"image"
	"image/color"

	"github.com/go-gfx/gfx/raster"
)

// jpegPixels turns a decoded JPEG into pixels, putting subsampled chroma back
// to full resolution the way every libjpeg-based PDF reader does.
//
// # WHY THIS EXISTS
//
// A JPEG that stores its chroma at half resolution — 4:2:0, which is what
// nearly every colour JPEG in the corpus is — has to have that chroma put back
// before it can be turned into RGB, and ISO/IEC 10918 does not say how. There
// are two answers in the field:
//
//   - REPLICATE each chroma sample over the pixels it covers. That is what
//     [image.YCbCr] does: its COffset divides x and y by the sampling factor
//     (image/ycbcr.go:101), so four pixels share one chroma sample exactly. It
//     is also what pdf.js does — src/core/jpg.js truncates with
//     "0 | (x * componentScaleX)".
//   - INTERPOLATE, weighting the nearer chroma sample 3 and the further one 1
//     in each direction. That is libjpeg's "fancy upsampling", which is on by
//     default (jdapimin.c:229), and so it is what poppler (DCTStream.cc:98
//     never touches the flag), pdfium, mupdf and Ghostscript all do.
//
// The two are not close. Both were measured against poppler over the whole
// conformance corpus for go-pdfkit/render#40: replication differs from it by
// 16 to 62 levels of 255 over a third to a half of a 4:2:0 picture's pixels,
// and interpolation differs by at most 3, on a hundredth of a percent of them.
// Every other part of the decode already agreed to the bit — a greyscale JPEG
// and a 4:4:4 one come out of Go's decoder byte for byte identical to
// libjpeg's, because neither has any chroma to put back.
//
// It reproduces libjpeg's filter only where libjpeg applies it: 4:2:0, 4:2:2
// and 4:4:0, and within those only when the chroma plane is more than two
// samples wide, because below that libjpeg falls back to replication
// (jdsample.c:503 and :534, "compptr->downsampled_width > 2"). 4:1:1 and 4:1:0
// have no fancy method in libjpeg at all — they go through int_upsample, which
// replicates (jdsample.c:552) — so they are left alone here too.
func jpegPixels(img image.Image) *raster.Image {
	yc, ok := img.(*image.YCbCr)
	if !ok {
		return raster.FromImage(img)
	}
	hf, vf := chromaFactors(yc.SubsampleRatio)
	b := yc.Bounds()
	w, h := b.Dx(), b.Dy()
	// The dimensions libjpeg's upsampler reads, its downsampled_width and
	// downsampled_height. The planes are wider than this when the picture does
	// not fill its last MCU, and what is past them is padding rather than
	// picture: libjpeg reconstructs an edge by repeating the last real sample
	// (jdmainct.c:217), not by reading the padding.
	cw, ch := (w+hf-1)/hf, (h+vf-1)/vf
	if hf*vf == 1 || cw <= 2 {
		return raster.FromImage(img)
	}
	cb := chromaRows(yc.Cb, yc, cw, ch, hf, vf)
	cr := chromaRows(yc.Cr, yc, cw, ch, hf, vf)
	out := raster.New(w, h)
	for y := 0; y < h; y++ {
		u, v := cb.at(y), cr.at(y)
		for x := 0; x < w; x++ {
			l := yc.Y[yc.YOffset(b.Min.X+x, b.Min.Y+y)]
			r, g, bl := color.YCbCrToRGB(l, u[x], v[x])
			o := (y*w + x) * 4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = r, g, bl, 255
		}
	}
	return out
}

// chromaFactors is how many pixels one chroma sample covers each way, and is
// 1 by 1 — meaning "leave it to [raster.FromImage]" — for every ratio libjpeg
// reconstructs by replication rather than by interpolating.
func chromaFactors(r image.YCbCrSubsampleRatio) (hf, vf int) {
	switch r {
	case image.YCbCrSubsampleRatio420:
		return 2, 2
	case image.YCbCrSubsampleRatio422:
		return 2, 1
	case image.YCbCrSubsampleRatio440:
		return 1, 2
	}
	return 1, 1
}

// A plane is one chroma plane and how to read a full-width row out of it.
//
// The rows are made one at a time into a buffer this owns rather than into a
// second picture, because a picture is already the largest thing a page
// allocates and reconstructing two whole planes beside it would raise what a
// JPEG costs by half.
type plane struct {
	src          []uint8
	base, stride int
	cw, ch       int
	hf, vf       int
	buf          []uint8
}

// chromaRows is the row source for one plane of a subsampled picture.
func chromaRows(src []uint8, yc *image.YCbCr, cw, ch, hf, vf int) *plane {
	b := yc.Bounds()
	return &plane{src: src, base: yc.COffset(b.Min.X, b.Min.Y), stride: yc.CStride,
		cw: cw, ch: ch, hf: hf, vf: vf, buf: make([]uint8, 2*cw)}
}

// row is one row of stored chroma, with an index past either end reading the
// row at that end. libjpeg reconstructs a picture's edge from a duplicate of
// its last real row rather than from the padding that follows it in memory
// (jdmainct.c:217), and the padding is what the difference would be made of.
func (p *plane) row(cy int) []uint8 {
	if cy < 0 {
		cy = 0
	}
	if cy >= p.ch {
		cy = p.ch - 1
	}
	o := p.base + cy*p.stride
	return p.src[o : o+p.cw]
}

// at is one full-width row of chroma for output row y.
func (p *plane) at(y int) []uint8 {
	switch {
	case p.vf == 1:
		h2v1Row(p.buf, p.row(y), p.cw)
	case p.hf == 1:
		// The upper output row of a pair takes its second-nearest row from
		// above and the lower one from below, and the two round the other way
		// from each other (jdsample.c, h1v2_fancy_upsample).
		h1v2Row(p.buf, p.row(y/2), p.row(farRow(y)), p.cw, 1+y%2)
	default:
		h2v2Row(p.buf, p.row(y/2), p.row(farRow(y)), p.cw)
	}
	return p.buf
}

// farRow is the second-nearest chroma row of output row y, which is the one
// above for the upper of a pair and the one below for the lower.
func farRow(y int) int {
	if y%2 == 0 {
		return y/2 - 1
	}
	return y/2 + 1
}

// h2v1Row is libjpeg's h2v1_fancy_upsample for one row: three quarters of the
// nearer chroma sample and one quarter of the further one.
func h2v1Row(dst, in []byte, cw int) {
	dst[0] = in[0]
	dst[1] = byte((int(in[0])*3 + int(in[1]) + 2) >> 2)
	for c := 1; c < cw-1; c++ {
		v := int(in[c]) * 3
		dst[2*c] = byte((v + int(in[c-1]) + 1) >> 2)
		dst[2*c+1] = byte((v + int(in[c+1]) + 2) >> 2)
	}
	dst[2*cw-2] = byte((int(in[cw-1])*3 + int(in[cw-2]) + 1) >> 2)
	dst[2*cw-1] = in[cw-1]
}

// h1v2Row is libjpeg's h1v2_fancy_upsample for one row, which interpolates
// down the column only. The bias is 1 for the upper row of a pair and 2 for
// the lower, which is how libjpeg keeps the pair's rounding from drifting.
func h1v2Row(dst, near, far []byte, cw, bias int) {
	for c := 0; c < cw; c++ {
		dst[c] = byte((int(near[c])*3 + int(far[c]) + bias) >> 2)
	}
}

// h2v2Row is libjpeg's h2v2_fancy_upsample for one row: nine sixteenths of the
// nearest chroma sample, three of each neighbour and one of the diagonal.
func h2v2Row(dst, near, far []byte, cw int) {
	col := func(c int) int { return int(near[c])*3 + int(far[c]) }
	last := col(0)
	dst[0] = byte((last*4 + 8) >> 4)
	this := col(1)
	dst[1] = byte((last*3 + this + 7) >> 4)
	for c := 1; c < cw-1; c++ {
		next := col(c + 1)
		dst[2*c] = byte((this*3 + last + 8) >> 4)
		dst[2*c+1] = byte((this*3 + next + 7) >> 4)
		last, this = this, next
	}
	dst[2*cw-2] = byte((this*3 + last + 8) >> 4)
	dst[2*cw-1] = byte((this*4 + 7) >> 4)
}
