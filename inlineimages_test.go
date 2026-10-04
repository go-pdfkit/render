package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// TestImagesReturnsTheInlineOnes is the measurement that made this change.
//
// Images returned nothing for a picture written into the content stream, and
// said why: it is the object of nothing. pdfimages extracts them and lists
// them with an object of 0, so over the 450 forms of the conformance corpus's
// fr-cerfa there were 4 147 pictures the reference got out and nothing of ours
// was ever compared with. See go-pdfkit/render#101.
func TestImagesReturnsTheInlineOnes(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	ims, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 {
		t.Fatalf("got %d pictures", len(ims))
	}
	im := ims[0]
	// Object 0 is what the file gives it and what pdfimages prints, and it is
	// what sends a harness pairing by object number to its size fallback.
	if im.Object != 0 {
		t.Errorf("object %d, want 0", im.Object)
	}
	// A name of its own, because there is no resource name and a reader of a
	// difference has to be able to say which picture it was.
	if im.Name != "BI#1" {
		t.Errorf("name %q", im.Name)
	}
	if im.Pic.W != 2 || im.Pic.H != 1 {
		t.Errorf("%dx%d, want 2x1", im.Pic.W, im.Pic.H)
	}
	if im.Stencil {
		t.Error("an image was called a stencil")
	}
}

func TestEachInlineImageGetsItsOwnName(t *testing.T) {
	// ONE ENTRY PER BI, and the ordinal counts across the whole call: an
	// inline image is its own draw, there is no object to collapse repeats
	// onto, and pdfimages lists one row per draw too. A page that stamps the
	// same bytes three times is three rows on both sides.
	one := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q "
	d := onePage(t, [4]float64{0, 0, 20, 20}, strings.Repeat(one, 3), nil)
	ims, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, im := range ims {
		names = append(names, im.Name)
	}
	if fmt.Sprint(names) != "[BI#1 BI#2 BI#3]" {
		t.Errorf("names %v", names)
	}
}

func TestAnInlineImageInsideAFormIsReturnedToo(t *testing.T) {
	// The ordinal has to count across streams, or two of them share a name
	// within one call. A form draws one and the page draws one.
	form := reader.Dict{}
	d := pageWithForm(t,
		"q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q /F Do",
		form,
		"q 20 0 0 20 0 0 cm BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI Q")
	ims, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 2 {
		t.Fatalf("got %d pictures: %+v", len(ims), ims)
	}
	if ims[0].Name != "BI#1" || ims[1].Name != "BI#2" {
		t.Errorf("names %q and %q", ims[0].Name, ims[1].Name)
	}
}

func TestAnInlineStencilIsReportedAsOne(t *testing.T) {
	// JBIG2 and CCITT stencils are most of what this change makes visible:
	// 2 614 of fr-cerfa's 4 147 are stencils, 2 592 of them 16x16.
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /IM true ID \x40 EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	ims, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 || !ims[0].Stencil {
		t.Fatalf("got %+v", ims)
	}
}

func TestAnInlineImageThatDecodesToNothingIsNotReturned(t *testing.T) {
	// Nothing comes back for a picture no codec here can read, which is the
	// same answer Page gives by not drawing it, and the ordinal must not
	// advance over it or the names of the ones that ARE returned have gaps a
	// reader cannot account for.
	content := "q 20 0 0 20 0 0 cm BI /W 0 /H 0 /BPC 8 /CS /G ID  EI Q " +
		"q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	ims, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ims) != 1 {
		t.Fatalf("got %d pictures: %+v", len(ims), ims)
	}
	if ims[0].Name != "BI#1" {
		t.Errorf("the ordinal counted a picture that was not returned: %q", ims[0].Name)
	}
}

func TestAPageOfInlineImagesPastTheBudgetIsRefusedWhole(t *testing.T) {
	// The budget is charged before the decode, as it is for an XObject, and a
	// page that cannot be decoded whole comes back as nothing rather than as
	// half of itself: half the pictures of a page read as the whole of them by
	// anything counting.
	one := fmt.Sprintf("q BI /W %d /H %d /BPC 8 /CS /G ID ", maxImagesPixels/2, 2)
	content := one + strings.Repeat("\x00", 8) + " EI Q " + one + strings.Repeat("\x00", 8) + " EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	if _, err := Images(d, 1); err == nil {
		t.Error("a page past the budget came back without an error")
	}
}
