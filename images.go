// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"errors"
	"fmt"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// Image is one picture a page draws, decoded.
type Image struct {
	// Name is the resource name the page draws it by.
	Name string
	// Object is the number of the indirect object the picture was stored in,
	// and nought when it has none: an inline image, or a stream written
	// directly into the resource dictionary rather than referred to.
	//
	// It is here because it is the one identity a picture and another tool's
	// extraction of it can be matched on, and this is the only place that
	// knows it without guessing. A caller left with the NAME has to walk the
	// document again to recover it, and a name is not an identity: it is
	// unique within one resource dictionary and not across the several a page
	// reaches through its forms, so `Im1` can mean two different objects on
	// one page. go-pdfkit/conformance rebuilt this number that way and needed
	// three mechanisms to do it -- a walk, an ambiguity rule for names that
	// reached two objects, and a mapping from a mask to the picture that names
	// it -- and each of the three produced a defect.
	//
	// A MASK carries the object of the picture that NAMES it, not its own.
	// That is what pdfimages publishes on an smask row, and it is the number a
	// mask can actually be paired on.
	Object int
	// Filter is the image format the picture was stored in — DCTDecode,
	// JPXDecode, JBIG2Decode, CCITTFaxDecode — and is empty when the bytes
	// were samples that no image codec had to read.
	Filter string
	// Decoded says the dictionary carried a /Decode array, which maps the
	// stored samples onto the range the colour space wants and is applied
	// here. It is worth knowing about because it is the one thing that makes
	// this picture differ from the same picture as another tool EXTRACTS it:
	// pdfimages writes the samples as stored, so a /Decode of [1 0] on a
	// one-bit mask makes the two exact complements of each other, and every
	// pixel differs for a reason that is not a disagreement.
	Decoded bool
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
// the ones written into the content stream with BI — are not returned: they
// are named by no resource and are objects of nothing, so there is nothing for
// a tool that extracts a file's objects to hand back beside them.
//
// It is the CONTENT STREAM that is walked, not the page's /Resources. A
// resource dictionary is a catalogue of what a page may draw, and PDF lets
// every page in a file share one; see [renderer.imagesDrawn] for what walking
// the catalogue instead did to a measurement.
//
// Each picture comes back once, however many of the page's forms reach it, and
// a page that draws more picture than [maxImagesPixels] is refused whole with
// [ErrTooMuchToDecode] rather than decoded until the machine gives out.
func Images(d *reader.Document, i int) ([]Image, error) {
	page, err := d.Page(i)
	if err != nil {
		return nil, err
	}
	r := &renderer{
		doc:       d,
		fonts:     map[int]*pdfFont{},
		softMasks: map[softMaskKey][]uint8{},
		seen:      map[reader.Ref]bool{},
		budget:    maxImagesPixels,
		bounded:   true,
	}
	res, _ := reader.ToDict(resolve(d, page.Get("Resources")))
	// The error is dropped for the reason [Page] drops it: it says the page
	// itself could not be read, and the page was read a few lines above.
	dec, _ := d.PageContentDecoded(i)
	out := r.imagesDrawn(dec.Data, res, 0)
	if r.refused != nil {
		return nil, r.refused
	}
	return out, nil
}

// maxImageDepth is how far a form XObject may nest before its pictures stop
// being counted. A form may hold a form; this bounds how deep the walk goes,
// and firstVisit below is what stops it going round.
const maxImageDepth = 8

// maxImagesPixels is how many pixels one call to [Images] may decode between
// all the pictures a page reaches, mask included. A picture costs four bytes a
// pixel, so this is a gigabyte.
//
// # EXPERIMENT
//
// Over the 2 268 real forms of the corpus — 10 659 pages, of which 2 111 draw
// a picture at all — the page naming the most comes to 31 814 093 pixels, or
// 127 MB once decoded. The median is 90 048. Not one page in the corpus names
// as much as a sixth of this limit, and the document that started this named
// enough for 87 GB.
const maxImagesPixels = 256 << 20

// ErrTooMuchToDecode says a page names more picture than [Images] will decode
// at once. Nothing comes back with it: half the pictures of a page would be
// read as the whole of them by anything counting.
var ErrTooMuchToDecode = errors.New("render: the page names more picture than may be decoded at once")

// firstVisit reports whether an XObject has not been walked before, and
// remembers it if not.
//
// A page's resources are a GRAPH and not a tree. In openpdf's
// pdfsmartcopy_bec.pdf the page names 37 form XObjects and every one of them
// names the page's own /Resources dictionary straight back, so a walk that
// descends every path visits 37 dictionaries at the first level, 1 369 at the
// second, 50 653 at the third — 37 to the eighth at the depth limit, which is
// three and a half million million — and decodes each of the page's 40
// pictures once per visit. That is where 87 GB came from, and it is the same
// arithmetic that decodes each of the two pictures of the French form
// cerfa_10103.pdf 511 times: that page's forms fan out by two, and the sum of
// two to the power nought through eight is 511.
//
// Entering each XObject once turns that back into one visit per object, which
// is what the file holds. It also means a picture two forms both reach comes
// back once rather than twice, which is the answer wanted anyway: the two
// entries would be the same stream decoded twice under the same name.
func (r *renderer) firstVisit(entry reader.Object) bool {
	ref, ok := entry.(reader.Ref)
	if !ok {
		// A stream written into the dictionary rather than referred to cannot
		// be reached a second way, so there is nothing to remember.
		return true
	}
	if r.seen[ref] {
		return false
	}
	r.seen[ref] = true
	return true
}

// afford takes the pixels a picture declares out of the budget, and reports
// whether there were enough.
//
// It is asked BEFORE the picture is decoded, because a limit noticed after the
// allocation has not helped: decodeBase makes four bytes for every pixel of
// the declared /Width by /Height whatever the stream turns out to hold, so a
// document names the memory rather than carrying it — a 208 KB file names 87
// GB. What is charged is therefore what the file declares and not what the
// codec produced, and a picture charged for and then found undecodable is not
// refunded. That is the conservative direction and the only one that can be
// checked in time.
func (r *renderer) afford(dict reader.Dict) bool {
	w := intOr(resolve(r.doc, dict.Get("Width")), 0)
	h := intOr(resolve(r.doc, dict.Get("Height")), 0)
	if w <= 0 || h <= 0 {
		// decodeBase hands this one back as nothing without allocating.
		return true
	}
	// Either side on its own past the whole budget is refused before the two
	// are multiplied, since a file may declare a width that overflows.
	if w > maxImagesPixels || h > maxImagesPixels || w*h > int64(r.budget) {
		r.refused = fmt.Errorf("%w: a picture of %d by %d pixels, with %d of the %d pixels left",
			ErrTooMuchToDecode, w, h, r.budget, maxImagesPixels)
		return false
	}
	r.budget -= int(w * h)
	return true
}

// decoded reads one image XObject and the mask it names, if any.
func (r *renderer) decoded(name string, object int, st *reader.Stream, res reader.Dict) []Image {
	var out []Image
	if !r.afford(st.Dict) {
		return nil
	}
	if s := r.decodeBase(st.Dict, st.Raw, res); s != nil {
		stencil, _ := reader.ToBool(resolve(r.doc, st.Dict.Get("ImageMask")))
		out = append(out, Image{
			Name:    name,
			Object:  object,
			Filter:  imageFilterOf(r.doc, st),
			Decoded: r.hasDecodeArray(st.Dict),
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
		if !r.afford(ms.Dict) {
			return out
		}
		s := r.decodeBase(ms.Dict, ms.Raw, res)
		if s == nil {
			continue
		}
		out = append(out, Image{
			Name: name + "/" + string(key),
			// The PARENT's object, deliberately: a mask is listed under the
			// number of the picture that names it, and its own is of no use to
			// anybody trying to pair it.
			Object:  object,
			Filter:  imageFilterOf(r.doc, ms),
			Decoded: r.hasDecodeArray(ms.Dict),
			Stencil: true,
			Pic:     &raster.Image{W: s.w, H: s.h, Pix: s.pix},
		})
	}
	return out
}

// hasDecodeArray says whether a picture's samples were mapped through a
// /Decode array on the way out.
func (r *renderer) hasDecodeArray(dict reader.Dict) bool {
	_, ok := reader.ToArray(resolve(r.doc, dict.Get("Decode")))
	return ok
}

// imageFilterOf names the image format a stream's filter chain stopped at, and
// is empty when the chain ran to samples.
func imageFilterOf(d *reader.Document, st *reader.Stream) string {
	return string(reader.DecodeRecovering(st.Dict, st.Raw, d.Resolver()).Image)
}

// imagesDrawn collects the pictures a content stream draws, in the order it
// draws them by, descending into the forms it draws.
//
// It replaces a walk over the page's /Resources, which is a catalogue of what
// the page MAY draw and not a record of what it does. The two are the same
// thing often enough that the difference went unnoticed, and completely
// different when a file gives every page one shared resource dictionary — as
// the French tax forms do. 2044_2044_4764.pdf names ten 118 by 118 Data Matrix
// barcodes, one per page; its page 1 draws exactly one of them and the walk
// returned all ten.
//
// That is not merely a longer list. Measuring against a judge that extracts
// what a page DRAWS, the nine extra pictures had nothing to be paired with, and
// one of them was matched to the drawn barcode's row because they are the same
// size — so two different barcodes were compared, came out 255 apart, and the
// one picture that WAS drawn was left unpaired and unmeasured. The extra
// entries did not dilute the measurement; they replaced it.
func (r *renderer) imagesDrawn(content []byte, res reader.Dict, depth int) []Image {
	if depth > maxImageDepth {
		return nil
	}
	xo, _ := reader.ToDict(resolve(r.doc, res.Get("XObject")))
	// A stream that stops decoding part way is walked as far as it got, which
	// is what this package draws; the error says nothing the operators do not.
	ops, _ := reader.Operations(content)
	var out []Image
	for _, op := range ops {
		if op.Operator != "Do" || len(op.Operands) == 0 {
			continue
		}
		name, ok := reader.ToName(op.Operands[len(op.Operands)-1])
		if !ok {
			continue
		}
		entry := xo.Get(name)
		st, ok := reader.ToStream(resolve(r.doc, entry))
		if !ok {
			continue
		}
		if !r.firstVisit(entry) {
			continue
		}
		sub, _ := reader.ToName(resolve(r.doc, st.Dict.Get("Subtype")))
		if sub == "Form" {
			// A form with no resources of its own draws against the ones in
			// force where it was drawn, which is what drawForm does.
			inner, ok := r.doc.GetDict(st.Dict, "Resources")
			if !ok {
				inner = res
			}
			c, img, err := r.salvaged(st)
			if err != nil || img != "" {
				continue
			}
			out = append(out, r.imagesDrawn(c, inner, depth+1)...)
			if r.refused != nil {
				return out
			}
			continue
		}
		if sub != "Image" {
			continue
		}
		object := 0
		if ref, ok := entry.(reader.Ref); ok {
			object = ref.Num
		}
		out = append(out, r.decoded(string(name), object, st, res)...)
		if r.refused != nil {
			return out
		}
	}
	return out
}
