package render

import (
	"crypto/sha256"
	"testing"

	"github.com/go-pdfkit/reader"
)

// TestTheSameFileDrawsTheSamePixelsEveryTime guards the dependency, not this
// package's own code.
//
// An inline image dictionary may carry both spellings of a key — /W beside
// /Width, /CS beside /ColorSpace — and disagree with itself. reader before
// v0.4.2 expanded such a dictionary in one pass over its map, so the winner
// was whichever Go's randomised iteration order yielded last, and the same
// page drew a different picture on different runs of the same binary. Eight
// renders of safedocs' Inline_Image_Abbreviations fixture gave five different
// answers.
//
// Nothing in this package's own tests could see that: every one of them draws
// a page once. Non-determinism is invisible to a suite that never repeats
// itself, which is why this test repeats itself.
//
// The image is built here rather than committed, so nobody else's PDF enters
// the repository. It says /W 2 and /Width 1, and /H 1 and /Height 2: the two
// readings give a 2x1 image and a 1x2 one, which do not cover the same pixels.
func TestTheSameFileDrawsTheSamePixelsEveryTime(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	content := "q 20 0 0 20 0 0 cm " +
		"BI /W 2 /Width 1 /H 1 /Height 2 /CS /G /ColorSpace /DeviceGray /BPC 8 " +
		"ID \x00\xff EI Q"
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": nums(0, 0, 20, 20),
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}

	var first string
	for i := 0; i < 40; i++ {
		d, err := reader.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(img.Pix)
		got := string(h[:])
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("draw %d differs from the first: the same file drew "+
				"different pixels, so something in the read path depends on "+
				"map iteration order", i)
		}
	}
}
