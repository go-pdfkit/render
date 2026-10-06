package render

import (
	"fmt"
	"image/color"
	"testing"
	"time"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// onePage builds a document of one page with the given content, media box and
// extra page entries, so a test can say exactly what is on the paper and then
// look at the pixels.
func onePage(t *testing.T, box [4]float64, content string, extra reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := reader.Dict{
		"Type":     reader.Name("Page"),
		"Parent":   pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"MediaBox": reader.Array{reader.Real(box[0]), reader.Real(box[1]),
			reader.Real(box[2]), reader.Real(box[3])},
	}
	for k, v := range extra {
		page[k] = v
	}
	pageRef := w.Add(page)
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// draw renders the first page of a document.
func draw(t *testing.T, d *reader.Document, opt Options) *raster.Image {
	t.Helper()
	img, err := Page(d, 1, opt)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// pixel names the colour of one pixel, for an error message.
func pixel(img *raster.Image, x, y int) string {
	c := img.At(x, y)
	return fmt.Sprintf("(%d,%d) = %d,%d,%d,%d", x, y, c.R, c.G, c.B, c.A)
}

// isBlack reports whether a pixel is fully inked.
func isBlack(img *raster.Image, x, y int) bool {
	c := img.At(x, y)
	return c.R < 40 && c.G < 40 && c.B < 40
}

// isWhite reports whether a pixel is bare paper.
func isWhite(img *raster.Image, x, y int) bool {
	c := img.At(x, y)
	return c.R > 220 && c.G > 220 && c.B > 220
}

// wantBlack asserts a pixel is inked.
func wantBlack(t *testing.T, img *raster.Image, x, y int) {
	t.Helper()
	if !isBlack(img, x, y) {
		t.Errorf("expected ink at %s", pixel(img, x, y))
	}
}

// wantWhite asserts a pixel is bare.
func wantWhite(t *testing.T, img *raster.Image, x, y int) {
	t.Helper()
	if !isWhite(img, x, y) {
		t.Errorf("expected paper at %s", pixel(img, x, y))
	}
}

// wantColour asserts a pixel is about the colour given.
func wantColour(t *testing.T, img *raster.Image, x, y int, want color.RGBA, tolerance int) {
	t.Helper()
	c := img.At(x, y)
	if abs(int(c.R)-int(want.R)) > tolerance ||
		abs(int(c.G)-int(want.G)) > tolerance ||
		abs(int(c.B)-int(want.B)) > tolerance {
		t.Errorf("%s, want %d,%d,%d", pixel(img, x, y), want.R, want.G, want.B)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// inked counts how many pixels are not bare paper.
func inked(img *raster.Image) int {
	n := 0
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			if !isWhite(img, x, y) {
				n++
			}
		}
	}
	return n
}

// pairedBest draws two documents ALTERNATELY and returns the shortest each
// took, which is what a test comparing the two should ask for.
//
// Three runs of one followed by three of the other is enough to fail on a busy
// machine: the witness can take all three of its turns in a quiet moment and
// the subject all three of its own under a spike, and the ratio then reports
// the machine rather than the code. Alternating puts every spike inside one
// ROUND, where it falls on both sides.
//
// Seen once, 2026-10-04: TestAThreeComponentJPEGIsCachedOnItsSampleTriple
// reported 8.9x at a load average of 59, and the same build passed three times
// in a row a minute later. The ratio was real and it was a ratio of two
// different moments.
//
// MEASURED, because that failure could not be reproduced on demand. A
// throwaway test computed the ratio both ways on the same work, forty rounds
// per scheme per run, under load:
//
//	                max of 40    max of 40    max of 40    over 3x
//	blocked              1.61         2.27         8.80    1 of 120
//	interleaved          1.53         1.52         1.61    0 of 120
//
// The 8.80 is the failure reproduced. The blocked scheme also returned 0.50
// once, which says the calibrated subject ran twice as fast as the device
// witness -- impossible for the quantity being measured, and the plainest
// demonstration that its two minima come from two different moments.
//
// A warm-up draw of each is the caller's business, and all three callers do it:
// the first draw of a page pays for whatever is cached once.
func pairedBest(t *testing.T, witness, subject *reader.Document, rounds int) (time.Duration, time.Duration) {
	t.Helper()
	w, s := time.Duration(1<<62), time.Duration(1<<62)
	for i := 0; i < rounds; i++ {
		start := time.Now()
		draw(t, witness, Options{})
		if took := time.Since(start); took < w {
			w = took
		}
		start = time.Now()
		draw(t, subject, Options{})
		if took := time.Since(start); took < s {
			s = took
		}
	}
	return w, s
}
