// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"errors"
	"fmt"
	"strings"
	"testing"

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
