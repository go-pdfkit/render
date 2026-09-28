package render

import (
	"image"
	imgcolor "image/color"
	"testing"

	"github.com/go-gfx/gfx/raster"
)

// greyAndItsFourByteForm builds the same picture twice: once at one byte a
// pixel, once at four. The bytes differ; the PICTURE must not.
func greyAndItsFourByteForm(w, h int) (*image.Gray, *image.RGBA) {
	g := image.NewGray(image.Rect(0, 0, w, h))
	r := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// A value that sweeps the whole range, so an off-by-one in the
			// palette shows somewhere rather than nowhere.
			v := uint8((y*w + x) % 256)
			g.SetGray(x, y, imgcolor.Gray{Y: v})
			r.SetRGBA(x, y, imgcolor.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return g, r
}

// TestAGreyScanDrawsTheSamePageAsTheFourByteOne is the whole claim, asked at the
// level a reader sees: the same page, drawn from the same picture handed over in
// the two shapes, is the same bytes.
//
// It is a differential rather than a fixture because the grey form exists to
// REPLACE the four-byte one on most of a scanned corpus. A fixture would pin
// what the grey path draws; only the pair pins that it draws what the path it
// replaces drew.
func TestAGreyScanDrawsTheSamePageAsTheFourByteOne(t *testing.T) {
	const w, h = 32, 32
	grey, rgba := greyAndItsFourByteForm(w, h)

	draw := func(img image.Image) *raster.Image {
		t.Helper()
		restore := jpxDecode
		jpxDecode = func([]byte) (image.Image, error) { return img, nil }
		defer func() { jpxDecode = restore }()

		// The data is never decoded -- the substitution above answers instead --
		// but it must still carry a size, which jpxSize reads.
		d := jpxPage(t, jpxImage(t, w, h), w, h)
		out, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	fromGrey, fromRGBA := draw(grey), draw(rgba)
	// Without this the comparison below is also satisfied by two blank pages.
	if inked(fromGrey) == 0 {
		t.Fatal("the grey page drew nothing, so identity says nothing")
	}
	if len(fromGrey.Pix) != len(fromRGBA.Pix) {
		t.Fatalf("pages of %d and %d bytes", len(fromGrey.Pix), len(fromRGBA.Pix))
	}
	for i := range fromGrey.Pix {
		if fromGrey.Pix[i] != fromRGBA.Pix[i] {
			t.Fatalf("byte %d of the page: grey gave %d, four-byte gave %d",
				i, fromGrey.Pix[i], fromRGBA.Pix[i])
		}
	}
}

// TestTheGreyFormIsHeldAtOneByteAPixel pins the reason the change exists. The
// test above would pass just as well if adoptedGrey never fired and the picture
// were converted -- the pixels would be identical and the memory unchanged.
func TestTheGreyFormIsHeldAtOneByteAPixel(t *testing.T) {
	const w, h = 8, 4
	grey, _ := greyAndItsFourByteForm(w, h)
	s := adoptedGrey(grey, w, h)
	if s == nil {
		t.Fatal("a densely packed *image.Gray was not adopted")
	}
	if s.pal == nil {
		t.Error("adopted without a palette, so its bytes are being read as colours")
	}
	if len(s.pix) != w*h {
		t.Errorf("pix is %d bytes for %d pixels, want one each", len(s.pix), w*h)
	}
	// And the bytes are the decoder's own, not a copy: that is what makes the
	// saving a saving rather than a second buffer.
	grey.Pix[0] = 77
	if got := s.at(0, 0); got.R != 77 {
		t.Errorf("at(0,0) = %+v after writing 77 into the decoder's buffer", got)
	}
	// Every entry of the palette is the grey the four-byte path wrote.
	for i, c := range greyPalette() {
		if want := (imgcolor.RGBA{R: uint8(i), G: uint8(i), B: uint8(i), A: 255}); c != want {
			t.Fatalf("palette[%d] = %+v, want %+v", i, c, want)
		}
	}
}

// TestTheGreyFormReachesTheDecoder asks the decoder what it built, because
// every other test here is satisfied by a grey picture that took the generic
// path: the pixels are the same either way, which is the point of the change
// and also what makes it invisible. Removing the call site broke nothing until
// this test existed.
func TestTheGreyFormReachesTheDecoder(t *testing.T) {
	const w, h = 8, 4
	grey, _ := greyAndItsFourByteForm(w, h)
	restore := jpxDecode
	jpxDecode = func([]byte) (image.Image, error) { return grey, nil }
	defer func() { jpxDecode = restore }()

	s := (&renderer{}).decodeJPX(jpxImage(t, w, h), w, h)
	if s == nil {
		t.Fatal("a grey codestream decoded to nothing")
	}
	if s.pal == nil || len(s.pix) != w*h {
		t.Errorf("held at %d bytes for %d pixels (palette %v), want one byte each",
			len(s.pix), w*h, s.pal != nil)
	}
}

// TestAdoptedGreyTakesOnlyWhatItMayTake covers each reason the decoder's buffer
// cannot be taken. None of these would fail loudly: they would hand back a
// picture read past its rows, or one whose rows are the wrong length, which
// looks like a shear rather than like an error.
func TestAdoptedGreyTakesOnlyWhatItMayTake(t *testing.T) {
	const w, h = 8, 4
	full, rgba := greyAndItsFourByteForm(w, h)

	sub := image.NewGray(image.Rect(0, 0, w+4, h+4)).SubImage(
		image.Rect(2, 2, 2+w, 2+h)).(*image.Gray)
	// Same stride, same size, offset only in Y: the stride check cannot see
	// this one, and it would adopt rows 0..h of a picture whose first row is h.
	shifted := image.NewGray(image.Rect(0, 0, w, h+4)).SubImage(
		image.Rect(0, 2, w, 2+h)).(*image.Gray)
	wide := image.NewGray(image.Rect(0, 0, w+3, h))
	padded := &image.Gray{Pix: make([]uint8, (w+2)*h), Stride: w + 2,
		Rect: image.Rect(0, 0, w, h)}
	narrow := &image.Gray{Pix: make([]uint8, w*h), Stride: w,
		Rect: image.Rect(0, 0, w-1, h)}
	flat := &image.Gray{Pix: make([]uint8, w*h), Stride: w,
		Rect: image.Rect(0, 0, w, h-1)}
	short := image.NewGray(image.Rect(0, 0, w, h))
	short.Pix = short.Pix[:w*h-1]

	for _, tc := range []struct {
		why  string
		img  image.Image
		w, h int
	}{
		{"not a grey picture at all", rgba, w, h},
		{"a sub-image, whose first byte is not its first pixel", sub, w, h},
		{"a sub-image offset in Y alone, which has the right stride", shifted, w, h},
		{"a wider picture, whose rows are not w long", wide, w, h},
		{"rows padded past their pixels, which only the stride can show", padded, w, h},
		{"a header narrower than its own stride", narrow, w, h},
		{"a header shorter than the buffer it points at", flat, w, h},
		{"the width the dictionary claims is not the width decoded", full, w + 1, h},
		{"the height the dictionary claims is not the height decoded", full, w, h + 1},
		{"a buffer shorter than its own picture", short, w, h},
	} {
		if s := adoptedGrey(tc.img, tc.w, tc.h); s != nil {
			t.Errorf("adopted %s: %d bytes for %dx%d", tc.why, len(s.pix), s.w, s.h)
		}
	}

	// The control: the shape it is FOR is taken. Without this the six refusals
	// above are also satisfied by a function that refuses everything.
	if adoptedGrey(full, w, h) == nil {
		t.Error("refused the shape it exists for")
	}
}
