package render

import (
	"fmt"
	"image/color"
	"testing"

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
