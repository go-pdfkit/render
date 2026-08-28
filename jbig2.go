// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"bytes"
	"image"

	"github.com/dkrisman/gobig2"
	"github.com/go-pdfkit/reader"
)

// decodeJBIG2 turns a JBIG2 stream into the packed one-bit rows the rest of
// this file already reads, so a JBIG2 image goes on to be drawn by the same
// code as any other one-bit image and a JBIG2 stencil by the same code as any
// other stencil.
//
// JBIG2 is what a scanned page's ink is stored in, and it is almost never the
// page's content: it is the /Mask that gives the high-resolution ink layer its
// shape. Counting filters by what they encode as content put it in twenty
// documents; counting the images it SHAPES put it in four thousand.
//
// The two conventions are opposite. JBIG2 sets a bit where there is ink; a
// one-bit DeviceGray sample of 0 is black, and an image mask paints where its
// sample is 0. One inversion here serves both.
func (r *renderer) decodeJBIG2(dict reader.Dict, data []byte, w, h int) []byte {
	img, err := jbig2Decode(data, r.jbig2Globals(dict))
	if err != nil || img == nil {
		return nil
	}
	b := img.Bounds()
	if b.Dx() != w || b.Dy() != h {
		// The dictionary's size is the one the page's geometry was computed
		// from. A stream that decodes to another size is not this image.
		return nil
	}
	rowBytes := (w + 7) / 8
	out := make([]byte, rowBytes*h)
	for i := range out {
		out[i] = 0xff
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gr, gg, gb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if (gr*299+gg*587+gb*114)/1000 < 0x8000 {
				out[y*rowBytes+x/8] &^= 0x80 >> uint(x%8)
			}
		}
	}
	return out
}

// jbig2Globals returns the shared segments the stream's decode parameters name,
// which is where an encoder puts the symbol dictionary several pages draw from.
// /DecodeParms runs parallel to /Filter, so it may be one dictionary or an
// array with one entry per filter in the chain.
func (r *renderer) jbig2Globals(dict reader.Dict) []byte {
	parms := resolve(r.doc, dict.Get("DecodeParms"))
	if arr, ok := reader.ToArray(parms); ok {
		for _, v := range arr {
			if g := r.globalsFrom(resolve(r.doc, v)); g != nil {
				return g
			}
		}
		return nil
	}
	return r.globalsFrom(parms)
}

// globalsFrom reads the globals stream one decode-parameters dictionary names.
func (r *renderer) globalsFrom(parms reader.Object) []byte {
	d, ok := reader.ToDict(parms)
	if !ok {
		return nil
	}
	st, ok := reader.ToStream(resolve(r.doc, d.Get("JBIG2Globals")))
	if !ok {
		return nil
	}
	data, _, err := reader.DecodeStream(st, r.doc.Get)
	if err != nil {
		return nil
	}
	return data
}

// jbig2Decode is a variable so a test can watch what happens when a decoder
// refuses, which is the case that decides whether the image is drawn wrong or
// not drawn.
var jbig2Decode = func(data, globals []byte) (image.Image, error) {
	d, err := gobig2.NewDecoderEmbedded(bytes.NewReader(data), globals)
	if err != nil {
		return nil, err
	}
	img, err := d.Decode()
	if err != nil {
		return nil, err
	}
	return img, nil
}
