// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"fmt"
	"sort"
	"testing"

	"github.com/go-pdfkit/reader"
)

// pageWithResources builds a page whose resource dictionary is whatever the
// caller gives, so a test can put forms, nonsense and pictures side by side.
func pageWithResources(t *testing.T, build func(w *reader.Writer) reader.Dict) *reader.Document {
	t.Helper()
	return pageContent(t, build, draws)
}

// pageContent is the same, with the content stream the caller's business: a
// test that means "this is in the resources and is NOT drawn" cannot say it
// through a builder that draws everything.
func pageContent(t *testing.T, build func(w *reader.Writer) reader.Dict, body func(reader.Dict) []byte) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	res := build(w)
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: body(res)}),
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

// draws is a content stream that draws every XObject the resources name, which
// is what these tests mean when they put a picture in a page: [Images] reads
// what a page DRAWS, and a resource dictionary is a catalogue of what it may.
// The names are sorted so a map's order cannot decide what a test measures.
func draws(res reader.Dict) []byte {
	xo, _ := reader.ToDict(res.Get("XObject"))
	names := make([]string, 0, len(xo))
	for name := range xo {
		names = append(names, string(name))
	}
	sort.Strings(names)
	var out []byte
	for _, n := range names {
		out = append(out, "/"+n+" Do\n"...)
	}
	return out
}

// form is a form XObject that draws every picture its own resources name, so
// a test can put a picture one level down and have the page reach it.
func form(w *reader.Writer, res reader.Dict) reader.Object {
	return w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"Resources": res,
	}, Raw: draws(res)})
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
		inner := form(w, reader.Dict{"XObject": reader.Dict{"Deep": greyImage(w)}})
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
		res := reader.Dict{"XObject": reader.Dict{"Loop": ref, "Pic": greyImage(w)}}
		w.Put(ref, &reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"Resources": res,
		}, Raw: draws(res)})
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

func TestAMaskComesBackBesideThePictureItShapes(t *testing.T) {
	// A picture that names a mask is returned unmasked, with the mask beside
	// it. Applying it to one side and not the other made 21 of 22 JPEG 2000
	// pictures in a corpus of scanned pages look wrong, when 11 of them
	// differed by nothing but the /SMask.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		mask := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
		}, Raw: []byte{0x00, 0x00}})
		return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"SMask": mask,
		}, Raw: []byte{0x00, 0xff}})}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d pictures, want the picture and its mask", len(got))
	}
	// The mask says nothing shows. The picture must come back anyway, opaque:
	// that is the codec's answer, and the codec is what this is about.
	if got[0].Name != "I" || got[0].Pic.Pix[3] != 255 {
		t.Errorf("the picture came back masked: %+v alpha %d", got[0], got[0].Pic.Pix[3])
	}
	if got[1].Name != "I/SMask" || !got[1].Stencil {
		t.Errorf("the mask came back as %+v", got[1])
	}
}

func TestAPictureWhoseMaskCannotBeReadStillComesBack(t *testing.T) {
	// Page declines to draw this one, because how much of it shows is
	// unknown. The codec read it, and this is about the codec.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		bad := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"Filter": reader.Name("JPXDecode"),
		}, Raw: []byte{0xff, 0x4f, 0xff, 0x51, 0, 1}})
		return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"SMask": bad,
		}, Raw: []byte{0x00, 0xff}})}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "I" {
		t.Fatalf("got %+v", got)
	}
	// And the page still refuses to draw it, which is the other half of the
	// rule and must not have moved.
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from an image whose mask cannot be read", ink)
	}
}

func TestAMaskThatIsNotAPictureIsNotOne(t *testing.T) {
	// /Mask may be an array of colour ranges rather than a stream, which is a
	// different mechanism and not a picture to hand back.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"Mask": reader.Array{reader.Integer(0), reader.Integer(0)},
		}, Raw: []byte{0x00, 0xff}})}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("a colour-key mask came back as a picture: %+v", got)
	}
}

func TestAMaskNothingCanDecodeIsLeftOut(t *testing.T) {
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		bad := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(0), "Height": reader.Integer(0),
		}, Raw: []byte{}})
		return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"Mask": bad,
		}, Raw: []byte{0x00, 0xff}})}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("a mask of no size came back as a picture: %+v", got)
	}
}

func TestAPictureSaysWhetherADecodeArrayShapedIt(t *testing.T) {
	// A /Decode of [1 0] on a one-bit mask makes this picture and the same
	// picture as pdfimages EXTRACTS it exact complements of each other: every
	// pixel differs, and none of it is a disagreement. Whoever compares the
	// two has to be able to tell that case apart from a real one.
	for _, tc := range []struct {
		name  string
		extra reader.Dict
		want  bool
	}{
		{"no array at all", nil, false},
		{"an array", reader.Dict{"Decode": reader.Array{reader.Integer(1), reader.Integer(0)}}, true},
		{"something that is not an array", reader.Dict{"Decode": reader.Integer(1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dict := reader.Dict{
				"Width": reader.Integer(2), "Height": reader.Integer(1),
				"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			}
			for k, v := range tc.extra {
				dict[k] = v
			}
			d := pageWithImage(t, dict, []byte{0x00, 0xff}, "")
			got, err := Images(d, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Decoded != tc.want {
				t.Errorf("got %+v, want Decoded=%v", got, tc.want)
			}
		})
	}
}

func TestAMaskSaysSoToo(t *testing.T) {
	// The mask is where this actually happens: 22 of the 54 JBIG2 soft masks
	// in a corpus of scanned medical documents carry /Decode [1 0], and not
	// one of the 194 stencils does.
	d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
		mask := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"Decode": reader.Array{reader.Integer(1), reader.Integer(0)},
		}, Raw: []byte{0x00, 0xff}})
		return reader.Dict{"XObject": reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			"SMask": mask,
		}, Raw: []byte{0x00, 0xff}})}}
	})
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Decoded || !got[1].Decoded {
		t.Errorf("got %+v", got)
	}
}

// TestAPageReturnsWhatItDrawsNotWhatItsResourcesHold is the shape that made
// this walk wrong: one resource dictionary shared by every page.
//
// A resource dictionary is a catalogue of what a page MAY draw. PDF lets every
// page in a file share one, and the French tax forms do: 2044_2044_4764.pdf
// gives all ten of its pages the same dictionary, holding ten 118 by 118 Data
// Matrix barcodes, one per page. Walking the dictionary handed page 1 all ten.
//
// Measured against a judge that extracts what a page draws, the nine extras
// were not merely noise. One of them was matched to the drawn barcode's row —
// the two are the same size — so two different barcodes were compared and came
// out 255 apart, and the barcode the page actually draws was left unmeasured.
// Corpus-wide, 30 of the 41 structural JPEG disagreements were pairs like that.
func TestAPageReturnsWhatItDrawsNotWhatItsResourcesHold(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	resRef := w.Reserve()
	w.Put(resRef, reader.Dict{"XObject": reader.Dict{
		"A": greyImage(w), "B": greyImage(w)}})
	page := func(body string) reader.Object {
		return w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
			"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(body)}),
			"Resources": resRef,
		})
	}
	first, second := page("/A Do\n"), page("/B Do\n")
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{first, second}, "Count": reader.Integer(2)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		page int
		want string
	}{{1, "A"}, {2, "B"}} {
		got, err := Images(d, tc.page)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("page %d: %d pictures, want just the one it draws", tc.page, len(got))
		}
		if got[0].Name != tc.want {
			t.Errorf("page %d drew %q, got %q", tc.page, tc.want, got[0].Name)
		}
	}
}

// TestAFormWithNoResourcesOfItsOwnDrawsAgainstThePage is what drawForm does,
// and the walk has to agree with it: a form whose dictionary carries no
// /Resources names its pictures out of the ones in force where it was drawn.
// Without this the picture is simply not found, and a page that draws fine
// comes back empty.
func TestAFormWithNoResourcesOfItsOwnDrawsAgainstThePage(t *testing.T) {
	d := pageContent(t, func(w *reader.Writer) reader.Dict {
		bare := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		}, Raw: []byte("/Pic Do\n")})
		return reader.Dict{"XObject": reader.Dict{"F": bare, "Pic": greyImage(w)}}
		// The page draws the form and nothing else, so the only way to the
		// picture is through the form's inherited resources.
	}, func(reader.Dict) []byte { return []byte("/F Do\n") })
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Pic" {
		t.Fatalf("got %d pictures %v, want the one the form drew", len(got), names(got))
	}
}

// names is what a failure needs to read to be actionable.
func names(ims []Image) []string {
	out := make([]string, 0, len(ims))
	for _, im := range ims {
		out = append(out, im.Name)
	}
	return out
}

// TestTheWalkStopsAtTheDepthLimit pins how far a form may hold a form. The
// self-referring case stops at the first repeat; this one never repeats, so
// only the bound can stop it.
func TestTheWalkStopsAtTheDepthLimit(t *testing.T) {
	const levels = 12
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	// Built from the bottom so each level can name the one below it.
	var below reader.Object
	var pageRes reader.Dict
	for i := levels - 1; i >= 0; i-- {
		xo := reader.Dict{reader.Name(fmt.Sprintf("Pic%d", i)): greyImage(w)}
		if below != nil {
			xo["Next"] = below
		}
		res := reader.Dict{"XObject": xo}
		if i == 0 {
			pageRes = res
			break
		}
		below = form(w, res)
	}
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: draws(pageRes)}),
		"Resources": pageRes,
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
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	// The page is the first level, and each form below it one more, so the
	// bound is reached with one picture per level walked.
	if len(got) != maxImageDepth+1 {
		t.Fatalf("%d pictures from %d levels, want %d", len(got), levels, maxImageDepth+1)
	}
}

// TestADoWithNoNameDrawsNothing is a content stream saying Do about something
// that is not a resource name. Real files hold these — an operand written
// wrongly, or one the scanner could not read as a name — and the walk has to
// step over it rather than take it as a name and find nothing under it.
func TestADoWithNoNameDrawsNothing(t *testing.T) {
	d := pageContent(t, func(w *reader.Writer) reader.Dict {
		return reader.Dict{"XObject": reader.Dict{"Pic": greyImage(w)}}
	}, func(reader.Dict) []byte { return []byte("Do\n123 Do\n/Pic Do\n") })
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	// The picture after the two nonsense operators still comes back, which is
	// what says the walk stepped over them rather than stopped.
	if len(got) != 1 || got[0].Name != "Pic" {
		t.Fatalf("got %v, want the one picture drawn by name", names(got))
	}
}

// TestAFormThatCannotBeReadIsSteppedOver covers the two ways a form's content
// does not arrive: no filter would decode it, and it is filtered as an image —
// a JPEG where operators should be. [drawForm] declines to draw either, and
// the walk has to decline to descend without giving up on the rest of the page.
func TestAFormThatCannotBeReadIsSteppedOver(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter reader.Name
	}{
		{"no filter decodes it", "NoSuchDecode"},
		{"it is filtered as an image", "DCTDecode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pageWithResources(t, func(w *reader.Writer) reader.Dict {
				bad := w.Add(&reader.Stream{Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
					"Filter": tc.filter,
				}, Raw: []byte("not a stream of operators")})
				return reader.Dict{"XObject": reader.Dict{"F": bad, "Pic": greyImage(w)}}
			})
			got, err := Images(d, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Name != "Pic" {
				t.Fatalf("got %v, want the page's own picture", names(got))
			}
		})
	}
}
