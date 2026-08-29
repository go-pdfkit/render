// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"sort"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// Image is one picture a page draws, decoded.
type Image struct {
	// Name is the resource name the page draws it by.
	Name string
	// Filter is the image format the picture was stored in — DCTDecode,
	// JPXDecode, JBIG2Decode, CCITTFaxDecode — and is empty when the bytes
	// were samples that no image codec had to read.
	Filter string
	// Stencil says the picture is a one-bit mask, which carries no colours of
	// its own: it paints whatever colour is in force through its own shape.
	// Its Pic has that shape in the alpha channel and black everywhere else,
	// because the colour is not the image's to know.
	Stencil bool
	// Pic is the decoded picture.
	Pic *raster.Image
}

// Images decodes the pictures the i'th page draws, counting from one, in the
// order of the names it draws them by.
//
// It exists because a page is the wrong unit for two jobs. Getting the
// pictures out of a document is one. Finding out whether a codec is right is
// the other: a page is a composition, so one image decoded wrongly can be
// averaged away by everything drawn over and around it, and a per-page
// difference of a few percent says nothing about which image was wrong. That
// mattered here — a release went out drawing scanned pages dark because "ink
// appeared" was measured instead of "the right ink".
//
// What comes back is what each CODEC produced, not what the page draws. A
// picture that names a mask is returned unmasked, and the mask is returned
// beside it as its own entry, named for the key that named it. That is what
// pdfimages does, and it is the only way the two can be compared: applying a
// mask to one side and not the other made 21 of 22 JPEG 2000 pictures in a
// corpus of scanned pages look wrong, when 11 of them differed by nothing but
// the /SMask.
//
// It also means a picture whose mask cannot be read still comes back. [Page]
// declines to draw that one, because how much of it shows is unknown — but the
// codec read it, and this is about the codec.
//
// A picture nothing here can decode is left out rather than returned empty,
// which is the same answer [Page] gives by not drawing it. Inline images —
// the ones written into the content stream — are not returned: they belong to
// the stream that draws them rather than to the page's resources.
func Images(d *reader.Document, i int) ([]Image, error) {
	page, err := d.Page(i)
	if err != nil {
		return nil, err
	}
	r := &renderer{doc: d, fonts: map[int]*pdfFont{}, softMasks: map[softMaskKey][]uint8{}}
	res, _ := reader.ToDict(resolve(d, page.Get("Resources")))
	return r.imagesIn(res, 0), nil
}

// maxImageDepth is how far a form XObject may nest before its pictures stop
// being counted. A form may hold a form, and a document may say it holds
// itself.
const maxImageDepth = 8

// imagesIn collects the pictures one resource dictionary reaches, following
// the forms it names.
func (r *renderer) imagesIn(res reader.Dict, depth int) []Image {
	if depth > maxImageDepth {
		return nil
	}
	xo, _ := reader.ToDict(resolve(r.doc, res.Get("XObject")))
	names := make([]string, 0, len(xo))
	for name := range xo {
		names = append(names, string(name))
	}
	// A map hands its keys back in a different order every run, and a list of
	// pictures that reorders itself is not a measurement.
	sort.Strings(names)

	var out []Image
	for _, name := range names {
		st, ok := reader.ToStream(resolve(r.doc, xo.Get(reader.Name(name))))
		if !ok {
			continue
		}
		sub, _ := reader.ToName(resolve(r.doc, st.Dict.Get("Subtype")))
		if sub == "Form" {
			inner, _ := reader.ToDict(resolve(r.doc, st.Dict.Get("Resources")))
			out = append(out, r.imagesIn(inner, depth+1)...)
			continue
		}
		if sub != "Image" {
			continue
		}
		out = append(out, r.decoded(name, st, res)...)
	}
	return out
}

// decoded reads one image XObject and the mask it names, if any.
func (r *renderer) decoded(name string, st *reader.Stream, res reader.Dict) []Image {
	var out []Image
	if s := r.decodeBase(st.Dict, st.Raw, res); s != nil {
		stencil, _ := reader.ToBool(resolve(r.doc, st.Dict.Get("ImageMask")))
		out = append(out, Image{
			Name:    name,
			Filter:  imageFilterOf(r.doc, st),
			Stencil: bool(stencil),
			Pic:     &raster.Image{W: s.w, H: s.h, Pix: s.pix},
		})
	}
	// A mask is a picture in its own right, stored in its own filter, and it
	// is very often the one that is wrong: JBIG2 is almost never a page's
	// content and almost always its mask.
	for _, key := range []reader.Name{"SMask", "Mask"} {
		ms, ok := reader.ToStream(resolve(r.doc, st.Dict.Get(key)))
		if !ok {
			continue
		}
		s := r.decodeBase(ms.Dict, ms.Raw, res)
		if s == nil {
			continue
		}
		out = append(out, Image{
			Name:    name + "/" + string(key),
			Filter:  imageFilterOf(r.doc, ms),
			Stencil: true,
			Pic:     &raster.Image{W: s.w, H: s.h, Pix: s.pix},
		})
	}
	return out
}

// imageFilterOf names the image format a stream's filter chain stopped at, and
// is empty when the chain ran to samples.
func imageFilterOf(d *reader.Document, st *reader.Stream) string {
	return string(reader.DecodeRecovering(st.Dict, st.Raw, d.Resolver()).Image)
}
