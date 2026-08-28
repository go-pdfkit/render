package render

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	jpeg2000 "github.com/ajroetker/go-jpeg2000"
	"github.com/go-pdfkit/reader"
)

// jpxImage encodes a small picture as a JPEG 2000 codestream, losslessly, so a
// test can check the colours that come back. It is built here rather than
// committed: nobody else's scan enters the repository.
func jpxImage(t *testing.T, w, h int) []byte {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: 20, G: 20, B: 20, A: 255} // dark
			if x >= w/2 {
				c = color.RGBA{R: 240, G: 240, B: 240, A: 255} // light
			}
			src.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg2000.Encode(&buf, src, &jpeg2000.EncodeOptions{Lossless: true}); err != nil {
		t.Fatalf("encoding a JPEG 2000 to draw: %v", err)
	}
	return buf.Bytes()
}

// jpxPage builds a page whose only content is one JPEG 2000 image filling it —
// which is what a scanned page IS.
func jpxPage(t *testing.T, data []byte, w, h int) *reader.Document {
	t.Helper()
	wr := reader.NewWriter("1.7")
	pagesRef := wr.Reserve()
	img := wr.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(w), "Height": reader.Integer(h),
		"ColorSpace": reader.Name("DeviceRGB"), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("JPXDecode"),
	}, Raw: data})
	pageRef := wr.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 40, 40),
		"Resources": reader.Dict{"XObject": reader.Dict{"S": img}},
		"Contents": wr.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 40 0 0 40 0 0 cm /S Do Q")})})
	wr.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := wr.Finish(reader.Dict{"Root": wr.Add(reader.Dict{
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

func TestAScannedPageIsDrawn(t *testing.T) {
	// A page whose only content is a JPEG 2000 image came out blank. Over a
	// corpus of a thousand scanned documents that is 655 pages: every one of
	// the 250 biodiversity scans carries such an image, and 248 of the 250
	// medical ones.
	d := jpxPage(t, jpxImage(t, 32, 32), 32, 32)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if inked(img) == 0 {
		t.Fatal("a page whose only content is a JPEG 2000 image drew nothing")
	}
	// Dark on the left, light on the right — so a decoder that hands back a
	// flat or a mirrored picture is caught, not merely one that hands back
	// nothing.
	if !isBlack(img, 8, 20) {
		t.Errorf("the dark half is not dark: %s", pixel(img, 8, 20))
	}
	if !isWhite(img, 30, 20) {
		t.Errorf("the light half is not light: %s", pixel(img, 30, 20))
	}
}

func TestAPictureThatWillNotDecodeIsNotDrawn(t *testing.T) {
	// The rule the rest of this file follows: not drawn rather than drawn
	// wrong.
	d := jpxPage(t, []byte{0xFF, 0x4F, 0xFF, 0x51, 0, 1, 2}, 32, 32)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from a codestream that is not one", ink)
	}
}

func TestTheSizeComesFromThePictureNotTheDictionary(t *testing.T) {
	// A codestream carries its own size, and where the two disagree the one the
	// pixels are actually in is the one that can be drawn.
	d := jpxPage(t, jpxImage(t, 32, 32), 999, 7)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if inked(img) == 0 {
		t.Error("a dictionary that lies about the size stopped the page being drawn")
	}
}
