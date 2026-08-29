// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// pageWithResources builds a page whose resource dictionary is whatever the
// caller gives, so a test can put forms, nonsense and pictures side by side.
func pageWithResources(t *testing.T, build func(w *reader.Writer) reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	res := build(w)
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")}),
		"Resources": res,
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

// greyImage is a picture stored as plain samples, dark then light.
func greyImage(w *reader.Writer) reader.Object {
	return w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
	}, Raw: []byte{0x00, 0xff}})
}

func TestThePicturesAPageDrawsComeBackDecoded(t *testing.T) {
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		return reader.Dict{"XObject": reader.Dict{"I": greyImage(w)}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures, want 1", len(got))
	}
	im := got[0]
	if im.Name != "I" || im.Filter != "" || im.Stencil {
		t.Errorf("name %q, filter %q, stencil %v", im.Name, im.Filter, im.Stencil)
	}
	if im.Pic.W != 2 || im.Pic.H != 1 {
		t.Fatalf("decoded %dx%d, want 2x1", im.Pic.W, im.Pic.H)
	}
	// Dark then light, so a decoder handing back a flat or mirrored picture is
	// caught and not merely one handing back nothing.
	if im.Pic.Pix[0] >= 128 {
		t.Errorf("the dark pixel came back at %d", im.Pic.Pix[0])
	}
	if im.Pic.Pix[4] < 128 {
		t.Errorf("the light pixel came back at %d", im.Pic.Pix[4])
	}
}

func TestAPictureIsNamedByTheFilterItWasStoredIn(t *testing.T) {
	// Which codec read a picture is the question a conformance run asks, and
	// it cannot be answered from the pixels.
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(16), "Height": reader.Integer(8),
		"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(1),
		"Filter": reader.Name("JBIG2Decode"),
	}, jbig2Ink, "")
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures, want 1", len(got))
	}
	if got[0].Filter != "JBIG2Decode" {
		t.Errorf("filter %q", got[0].Filter)
	}
}

func TestAStencilSaysSoAndCarriesOnlyItsShape(t *testing.T) {
	// A stencil has no colours of its own: what comes back is the shape, and
	// the colour belongs to whoever draws it.
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"ImageMask": reader.Bool(true),
	}, []byte{0b01000000}, "")
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Stencil {
		t.Fatalf("%d pictures, stencil %v", len(got), len(got) == 1 && got[0].Stencil)
	}
	if got[0].Pic.Pix[3] != 255 {
		t.Errorf("the painted pixel is transparent")
	}
	if got[0].Pic.Pix[7] != 0 {
		t.Errorf("the unpainted pixel is opaque")
	}
}

func TestThePicturesInsideAFormAreFound(t *testing.T) {
	// A form is a page inside a page. pdfimages follows them, and a picture
	// that is only reachable through one is still a picture the page draws.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		inner := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": reader.Dict{"XObject": reader.Dict{"Deep": greyImage(w)}},
		}, Raw: []byte("")})
		return reader.Dict{"XObject": reader.Dict{"F": inner, "A": greyImage(w)}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d pictures, want 2", len(got))
	}
	// A map hands its keys back in a different order every run. The names are
	// walked in order, so "A" comes before the form named "F" whose picture is
	// "Deep" — and the answer is the same every time, which a measurement
	// needs and a map does not give.
	if got[0].Name != "A" || got[1].Name != "Deep" {
		t.Errorf("came back as %q then %q", got[0].Name, got[1].Name)
	}
}

func TestAFormThatHoldsItselfStops(t *testing.T) {
	// A document may say a form holds itself, and following that is a way of
	// never coming back.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		ref := w.Reserve()
		w.Put(ref, &reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": reader.Dict{"XObject": reader.Dict{
				"Loop": ref, "Pic": greyImage(w)}},
		}, Raw: []byte("")})
		return reader.Dict{"XObject": reader.Dict{"F": ref}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	// One picture per level it went down, and then it stopped.
	if len(got) == 0 {
		t.Fatal("the picture inside the form was not found at all")
	}
	if len(got) > maxImageDepth+1 {
		t.Errorf("it went down %d levels", len(got))
	}
}

func TestWhatIsNotAPictureIsNotReturned(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  func(w *reader.Writer) reader.Dict
	}{
		{"a page with no resources", func(*reader.Writer) reader.Dict { return nil }},
		{"resources naming no XObjects", func(*reader.Writer) reader.Dict {
			return reader.Dict{"Font": reader.Dict{}}
		}},
		{"an XObject entry that is not a dictionary", func(*reader.Writer) reader.Dict {
			return reader.Dict{"XObject": reader.Integer(3)}
		}},
		{"an entry that is not a stream", func(*reader.Writer) reader.Dict {
			return reader.Dict{"XObject": reader.Dict{"X": reader.Integer(3)}}
		}},
		{"a stream that is neither picture nor form", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"XObject": reader.Dict{"X": w.Add(&reader.Stream{
				Dict: reader.Dict{"Subtype": reader.Name("PS")}, Raw: []byte("")})}}
		}},
		{"a picture nothing here can decode", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"XObject": reader.Dict{"X": w.Add(&reader.Stream{
				Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
					"Width": reader.Integer(4), "Height": reader.Integer(4),
					"Filter": reader.Name("JPXDecode"),
				}, Raw: []byte{0xff, 0x4f, 0xff, 0x51, 0, 1}})}}
		}},
		{"a form with nothing in it", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"XObject": reader.Dict{"F": w.Add(&reader.Stream{
				Dict: reader.Dict{"Type": reader.Name("XObject"),
					"Subtype": reader.Name("Form")}, Raw: []byte("")})}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pageWithResources(t, tc.res)
			got, err := Images(d, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Errorf("%d pictures came back", len(got))
			}
		})
	}
}

func TestAPageThatIsNotThereHasNoPictures(t *testing.T) {
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict { return nil })
	if _, err := Images(d, 9); err == nil {
		t.Error("page nine of a one-page document came back without complaint")
	}
}
