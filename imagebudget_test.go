// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// sharedResourcesPage builds the shape that took 87 GB: a page whose
// /Resources names a number of form XObjects, every one of which names that
// same /Resources dictionary straight back, and a number of pictures beside
// them.
//
// It is openpdf's pdfsmartcopy_bec.pdf reduced — 208 KB, three pages, 37 forms
// and 40 pictures per page, and every form pointing home. Nothing about the
// pictures matters to the blow-up; the fan-out does all of it. Walked as a
// tree, forms=4 reaches 4^8 = 65 536 dictionaries at the depth limit and
// decodes every picture in each of them, which for the real file is
// 37^8 ≈ 3.5e12 visits and 40 pictures apiece.
func sharedResourcesPage(t *testing.T, forms, pictures int, pic func(*reader.Writer) reader.Object) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	resRef := w.Reserve()
	xobj := reader.Dict{}
	for i := 0; i < pictures; i++ {
		xobj[reader.Name(fmt.Sprintf("Im%d", i))] = pic(w)
	}
	for i := 0; i < forms; i++ {
		xobj[reader.Name(fmt.Sprintf("Tr%d", i))] = w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			// The whole of the defect is on this line: the form hands the walk
			// back the dictionary the walk is already in.
			"Resources": resRef,
		}, Raw: []byte("")})
	}
	w.Put(resRef, reader.Dict{"XObject": xobj})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")}),
		"Resources": resRef,
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
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

// hugePicture declares a picture of the largest size a single one may be, in a
// format nothing here reads, so that what is charged for it can be watched
// without a quarter of a gigabyte actually being made.
func hugePicture(w *reader.Writer) reader.Object {
	return w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8192), "Height": reader.Integer(8192),
		"Filter": reader.Name("JPXDecode"),
	}, Raw: []byte{0xff, 0x4f, 0xff, 0x51, 0, 1}})
}

func TestASharedResourceDictionaryIsWalkedOnce(t *testing.T) {
	// The cause. A page's resources are a graph, not a tree, and walking every
	// path through one visits fan-out to the power of the depth limit and
	// decodes every picture that many times. It is why cerfa_10103.pdf decodes
	// its two pictures 511 times — its forms fan out by two, and the sum of
	// two to the power nought through eight is 511 — and why the three-page
	// pdfsmartcopy_bec.pdf, 208 KB on disk, was still allocating at 87 GB.
	d := sharedResourcesPage(t, 4, 3, greyImage)
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Three pictures, once each. Walked as a tree it is 3 x (4^0 + ... + 4^8),
	// which is 262 143.
	if len(got) != 3 {
		t.Fatalf("%d pictures came back for a page holding three", len(got))
	}
	for i, im := range got {
		if want := fmt.Sprintf("Im%d", i); im.Name != want {
			t.Errorf("picture %d is named %q, want %q", i, im.Name, want)
		}
		if im.Pic.W != 2 || im.Pic.H != 1 {
			t.Errorf("picture %d came back %dx%d", i, im.Pic.W, im.Pic.H)
		}
	}
}

func TestImagesRefusesAPageNamingMorePictureThanItWillDecode(t *testing.T) {
	// The bound. Walked once, a document may still name more picture than the
	// machine will hold — and it names it rather than carrying it, since a
	// picture costs four bytes for every pixel of its declared size whatever
	// its stream turns out to hold. So the size is charged BEFORE the picture
	// is decoded: five of the largest a single picture may be come to more
	// than the gigabyte this will decode.
	d := sharedResourcesPage(t, 4, 5, hugePicture)
	got, err := Images(d, 1)
	if !errors.Is(err, ErrTooMuchToDecode) {
		t.Fatalf("a page naming %d pixels came back with err=%v and %d pictures",
			5*8192*8192, err, len(got))
	}
	if got != nil {
		t.Errorf("%d pictures came back with the refusal", len(got))
	}
	// The refusal has to say what was exceeded, or it cannot be acted on.
	for _, want := range []string{"8192", fmt.Sprint(maxImagesPixels)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
}

func TestAPictureOfAnImpossibleSizeIsRefusedBeforeItIsMade(t *testing.T) {
	// A file may declare a width that no arithmetic survives. It is refused on
	// either side alone, before the two are multiplied.
	for _, tc := range []struct {
		name  string
		w, h  int64
		panic bool
	}{
		{name: "a width past the whole budget", w: 1 << 40, h: 1},
		{name: "a height past the whole budget", w: 1, h: 1 << 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
				return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
					"Width": reader.Integer(tc.w), "Height": reader.Integer(tc.h),
					"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
				}, Raw: []byte{0x00}})}}
			})
			if _, err := Images(d, 1); !errors.Is(err, ErrTooMuchToDecode) {
				t.Fatalf("a picture of %d by %d came back with %v", tc.w, tc.h, err)
			}
		})
	}
}

func TestAMaskIsChargedForToo(t *testing.T) {
	// A mask is a picture in its own right and is decoded like one, so it is
	// charged like one. Refusing the picture and then making its mask anyway
	// would leave the largest thing on the page unbounded.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		mask := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(1 << 40), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
		}, Raw: []byte{0x00}})
		return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"SMask": mask,
		}, Raw: []byte{0x00, 0xff}})}}
	})
	if _, err := Images(d, 1); !errors.Is(err, ErrTooMuchToDecode) {
		t.Fatalf("a mask of a million million pixels came back with %v", err)
	}
}

func TestARefusalInsideAFormStopsTheWalk(t *testing.T) {
	// The picture that breaks the budget may be several forms down, and the
	// walk has to come back up rather than carry on with the next form.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		inner := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": reader.Dict{"XObject": reader.Dict{"Deep": hugePicture(w)}},
		}, Raw: []byte("")})
		xo := reader.Dict{"F": inner}
		// Four more of the largest a picture may be, named so they are walked
		// before the form: the budget is gone by the time the form is reached.
		for i := 0; i < 4; i++ {
			xo[reader.Name(fmt.Sprintf("A%d", i))] = hugePicture(w)
		}
		return reader.Dict{"XObject": xo}
	})
	if _, err := Images(d, 1); !errors.Is(err, ErrTooMuchToDecode) {
		t.Fatalf("came back with %v", err)
	}
}

func TestAnOrdinaryPageIsNotRefused(t *testing.T) {
	// A bound that refused real documents would be worse than none. Not one
	// page of the 10 659 in the corpus names as much as a sixth of it.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		return reader.Dict{"XObject": reader.Dict{"I": greyImage(w)}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatalf("an ordinary page was refused: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures came back", len(got))
	}
}

func TestFormsMayStillNestAsDeepAsTheyMay(t *testing.T) {
	// Entering each XObject once is what stops the walk going round; the depth
	// limit is what stops it going down. A chain of forms that are all
	// different is not a cycle, and it still has to end somewhere.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		// The deepest form holds the picture, and the chain is built upwards
		// so that the picture sits one level past the limit.
		deepest := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": reader.Dict{"XObject": reader.Dict{"TooDeep": greyImage(w)}},
		}, Raw: []byte("")})
		for i := 0; i < maxImageDepth; i++ {
			deepest = w.Add(&reader.Stream{Dict: reader.Dict{
				"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
				"Resources": reader.Dict{"XObject": reader.Dict{"F": deepest}},
			}, Raw: []byte("")})
		}
		return reader.Dict{"XObject": reader.Dict{"F": deepest}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a picture %d forms down came back: %+v", maxImageDepth+1, got)
	}
}

func TestAStreamWrittenIntoTheDictionaryIsNotRemembered(t *testing.T) {
	// Only an indirect reference can be reached twice; a stream written into
	// the dictionary itself has one way in, and there is nothing to remember.
	r := &renderer{seen: map[reader.Ref]bool{}}
	if !r.firstVisit(reader.Integer(3)) {
		t.Error("something that is not a reference was taken for one already seen")
	}
	if len(r.seen) != 0 {
		t.Errorf("it was remembered anyway: %v", r.seen)
	}
	ref := reader.Ref{Num: 7}
	if !r.firstVisit(ref) {
		t.Error("a reference not seen before was called seen")
	}
	if r.firstVisit(ref) {
		t.Error("a reference was entered twice")
	}
}

// lyingJPEG is a real, valid JPEG of eight pixels by eight whose frame header
// has been altered to claim 65 535 by 65 535.
//
// It is 376 bytes and it makes image/jpeg allocate four gigabytes: the decoder
// makes the whole picture the moment it reaches the start of scan, and only
// then finds the scan data missing. Whatever /Width and /Height the PDF
// declares is beside the point — the codec believes its own header.
func lyingJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	for i := 0; i+8 < len(data); i++ {
		if data[i] == 0xFF && data[i+1] == 0xC0 {
			data[i+5], data[i+6] = 0xFF, 0xFF // height
			data[i+7], data[i+8] = 0xFF, 0xFF // width
			// The header has to say what it is going to make, or this fixture
			// proves nothing.
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
			if err != nil || cfg.Width != 65535 || cfg.Height != 65535 {
				t.Fatalf("the patched header reads back as %dx%d, %v", cfg.Width, cfg.Height, err)
			}
			return data
		}
	}
	t.Fatal("no frame header to patch")
	return nil
}

func TestACodestreamIsNotBelievedBeforeItIsMeasured(t *testing.T) {
	// The other way a small file names a great deal of memory, and the one
	// that walks straight past a budget charged on the dictionary: the
	// dictionary says eight pixels by eight and the codestream says 65 535 by
	// 65 535, and it is the codestream the decoder allocates for.
	data := lyingJPEG(t)
	if len(data) > 1024 {
		t.Fatalf("the fixture is %d bytes; it is meant to be tiny", len(data))
	}
	d := jpegPage(t, data, nil)

	// The decoder must never be reached: it is the decoder that allocates,
	// and it does so before it discovers there is no scan data. Nothing came
	// back either way, both before this guard and after it, so what has to be
	// asserted is that the four gigabytes were never asked for.
	reached := false
	was := jpegDecode
	jpegDecode = func(b []byte) (image.Image, error) {
		reached = true
		return was(b)
	}
	defer func() { jpegDecode = was }()

	got, err := Images(d, 1)
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if len(got) != 0 {
		t.Errorf("%d pictures came back from a codestream of 4 294 836 225 pixels", len(got))
	}
	if reached {
		t.Error("Images handed the bytes to the decoder anyway")
	}

	// The page draws nothing rather than allocating four gigabytes for it.
	// Page keeps no picture and so is not charged, but the ceiling on a single
	// one applies to it all the same.
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from a codestream nothing may hold", ink)
	}
	if reached {
		t.Error("Page handed the bytes to the decoder anyway")
	}
}

func TestWhatACodecSaysItHoldsIsPaidForToo(t *testing.T) {
	// A ceiling on one picture is not a budget: a page of pictures each
	// declaring one pixel and each holding a large codestream would come to
	// as much as it liked. What the codec says it holds is charged for, less
	// whatever the dictionary already paid.
	const most = maxImagePixels
	for _, tc := range []struct {
		name           string
		cw, ch         int
		charged        int
		budget         int
		bounded        bool
		want           bool
		wantLeft       int
		wantRefusal    bool
		wantRefusalHas string
	}{
		{name: "a header that says nothing is left to the decoder",
			cw: 0, ch: 0, budget: 10, bounded: true, want: true, wantLeft: 10},
		{name: "a height that says nothing is left to the decoder",
			cw: 4, ch: 0, budget: 10, bounded: true, want: true, wantLeft: 10},
		{name: "a codestream past the ceiling on one picture",
			cw: 65535, ch: 65535, budget: most, bounded: true, want: false, wantLeft: most},
		{name: "a width alone past the ceiling",
			cw: most + 1, ch: 1, budget: most, bounded: true, want: false, wantLeft: most},
		{name: "a height alone past the ceiling",
			cw: 1, ch: most + 1, budget: most, bounded: true, want: false, wantLeft: most},
		{name: "a page keeps no picture and spends nothing",
			cw: 100, ch: 100, budget: 0, bounded: false, want: true, wantLeft: 0},
		{name: "a codestream no larger than the dictionary is already paid for",
			cw: 10, ch: 10, charged: 100, budget: 5, bounded: true, want: true, wantLeft: 5},
		{name: "a codestream larger than the dictionary pays the difference",
			cw: 10, ch: 10, charged: 60, budget: 100, bounded: true, want: true, wantLeft: 60},
		{name: "and is refused when it cannot",
			cw: 10, ch: 10, charged: 60, budget: 39, bounded: true, want: false, wantLeft: 39,
			wantRefusal: true, wantRefusalHas: "39"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &renderer{budget: tc.budget, bounded: tc.bounded}
			if got := r.affordDecoded(tc.cw, tc.ch, tc.charged); got != tc.want {
				t.Errorf("a codestream of %d by %d with %d left: %v, want %v",
					tc.cw, tc.ch, tc.budget, got, tc.want)
			}
			if r.budget != tc.wantLeft {
				t.Errorf("%d pixels left, want %d", r.budget, tc.wantLeft)
			}
			switch {
			case tc.wantRefusal && !errors.Is(r.refused, ErrTooMuchToDecode):
				t.Errorf("refused with %v", r.refused)
			case tc.wantRefusal && !strings.Contains(r.refused.Error(), tc.wantRefusalHas):
				t.Errorf("the refusal %q does not name %s", r.refused, tc.wantRefusalHas)
			case !tc.wantRefusal && r.refused != nil:
				t.Errorf("refused with %v when it should not have", r.refused)
			}
		})
	}
}

func TestACodestreamThatSaysNothingIsLeftToItsDecoder(t *testing.T) {
	// A header nothing can be read from is a decoder's problem, not a
	// budget's: it gives up long before it allocates. Both codecs are asked
	// the same way and both are asked something they cannot read.
	if w, h := jpegSize([]byte("not a JPEG")); w != 0 || h != 0 {
		t.Errorf("a JPEG header read out of nothing as %dx%d", w, h)
	}
	if w, h := jpxSize([]byte("not a codestream")); w != 0 || h != 0 {
		t.Errorf("a JPEG 2000 header read out of nothing as %dx%d", w, h)
	}
	// And a real one is read.
	if w, h := jpxSize(jpxImage(t, 6, 4)); w != 6 || h != 4 {
		t.Errorf("a real codestream of 6x4 read as %dx%d", w, h)
	}
}

func TestAJPXCodestreamIsMeasuredBeforeItIsDecoded(t *testing.T) {
	// The same guard on the other codec. jpxSize is a variable so that a
	// header claiming more than may be held can be put behind it without
	// having to encode four gigabytes of picture to say so.
	wasSize := jpxSize
	jpxSize = func([]byte) (int, int) { return 100000, 100000 }
	defer func() { jpxSize = wasSize }()
	reached := false
	wasDecode := jpxDecode
	jpxDecode = func(b []byte) (*raster.Image, error) {
		reached = true
		return wasDecode(b)
	}
	defer func() { jpxDecode = wasDecode }()

	d := jpxPage(t, jpxImage(t, 6, 4), 6, 4)
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("%d pictures came back from a codestream of ten thousand million pixels", len(got))
	}
	if reached {
		t.Error("the bytes were handed to the decoder anyway")
	}
}
