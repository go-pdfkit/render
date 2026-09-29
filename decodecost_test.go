package render

import (
	"bytes"
	"image"
	imgcolor "image/color"
	"testing"

	"image/jpeg"

	jpeg2000 "github.com/go-images/jpeg2000"
)

// TestAOneComponentCodestreamIsChargedForOneComponent is the change, asked the
// way a reader meets it: the SAME picture, claimed by its header to be the same
// size, draws or does not according to how many components it says it has.
//
// 9 449 by 13 701 is bulletinno38tasm.pdf of the measured corpus -- 129.5
// megapixels of one component. It came back BLANK, because the bound was a
// pixel count that assumed four bytes each, and a decode of one component
// measured 6.8 to 9.7 bytes a pixel on whole pages while three measured 20.2 to
// 21.1. It was paying for colour it does not have.
//
// The pair is the point. A test of the grey case alone would pass just as well
// if the bound had simply been raised for everything.
func TestAOneComponentCodestreamIsChargedForOneComponent(t *testing.T) {
	const w, h = 9449, 13701
	for _, tc := range []struct {
		why  string
		grey bool
		draw bool
	}{
		{"one component, at 10 bytes a pixel", true, true},
		{"three, at 24 -- which is what it used to pay", false, false},
	} {
		was := jpxSize
		jpxSize = func([]byte) (int, int, bool) { return w, h, tc.grey }
		// The codestream itself is small: what is on trial is the decision
		// taken from the HEADER, before anything is decoded, which is the only
		// place it can be taken in time.
		d := jpxPage(t, jpxImage(t, 32, 32), 32, 32)
		img, err := Page(d, 1, Options{Scale: 1})
		jpxSize = was
		if err != nil {
			t.Fatalf("%s: %v", tc.why, err)
		}
		if drew := inked(img) > 0; drew != tc.draw {
			t.Errorf("%s: drew=%v, want %v", tc.why, drew, tc.draw)
		}
	}
}

// TestTheSizeAndTheShapeComeFromTheCodestream pins that the shape is read where
// the size is, and not from the dictionary -- a dictionary can say DeviceRGB
// over a one-component codestream, and it is the codestream that will be
// decoded.
func TestTheSizeAndTheShapeComeFromTheCodestream(t *testing.T) {
	data := jpxImage(t, 8, 8)
	w, h, grey := jpxSize(data)
	if w != 8 || h != 8 {
		t.Fatalf("jpxSize said %dx%d", w, h)
	}
	if grey {
		t.Error("a three-component codestream was called one component")
	}
	if _, _, g := jpxSize([]byte("not a codestream")); g {
		t.Error("a header that could not be read was called one component")
	}
}

// greyJPX encodes a ONE-component codestream, so that jpxSize is asked the
// question on a real header rather than through a stub.
func greyJPX(t *testing.T, w, h int) []byte {
	t.Helper()
	src := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			src.SetGray(x, y, imgcolor.Gray{Y: uint8((x*7 + y*3) % 256)})
		}
	}
	var buf bytes.Buffer
	if err := jpeg2000.Encode(&buf, src, &jpeg2000.EncodeOptions{Lossless: true}); err != nil {
		t.Fatalf("encoding a one-component JPEG 2000: %v", err)
	}
	return buf.Bytes()
}

// TestJpxSizeReadsTheComponentCount asks the REAL function both answers. The
// test above substitutes jpxSize, so without this one a version that never
// reports a single component -- which puts every scan back to blank -- passes
// the whole suite.
func TestJpxSizeReadsTheComponentCount(t *testing.T) {
	for _, tc := range []struct {
		why  string
		data []byte
		grey bool
		w, h int
	}{
		{"one component", greyJPX(t, 8, 8), true, 8, 8},
		{"three components", jpxImage(t, 8, 8), false, 8, 8},
		{"a header that cannot be read", []byte("not a codestream"), false, 0, 0},
	} {
		w, h, grey := jpxSize(tc.data)
		if grey != tc.grey {
			t.Errorf("%s: grey=%v, want %v", tc.why, grey, tc.grey)
		}
		if w != tc.w || h != tc.h {
			t.Errorf("%s: %dx%d, want %dx%d", tc.why, w, h, tc.w, tc.h)
		}
	}
}

// TestAJPEGKeepsTheBoundItHad pins the half of this change that is meant NOT to
// move. A JPEG decodes to colour whatever it holds, so it is charged 24 and its
// ceiling is the pixel count it had before. Charging it as one component would
// raise that bound by 2.4x and nothing else in the suite would notice.
func TestAJPEGKeepsTheBoundItHad(t *testing.T) {
	r := &renderer{}
	const most = maxImagePixels
	if r.affordDecoded(8192, 8192, 0, decodeCostOther) != true {
		t.Error("a JPEG at the old bound is refused")
	}
	if r.affordDecoded(8192, 8193, 0, decodeCostOther) != false {
		t.Error("a JPEG one row past the old bound is admitted")
	}
	// And the number itself: 24 bytes a pixel against maxDecodeBytes is the
	// old pixel bound exactly, not approximately.
	if got := maxDecodeBytes / decodeCostOther; got != int64(most) {
		t.Errorf("the colour cost gives a bound of %d pixels, want %d", got, most)
	}

	// Through the DECODER, not just the predicate. Passing the one-component
	// cost at the JPEG call site raises its bound by 2.4x, and everything above
	// stays green while it does: the predicate is asked with the right number
	// here and with the wrong one there.
	// REAL JPEG bytes, or the test proves nothing: a refusal and a decode that
	// fails both leave the page blank, and stand-in data cannot tell them
	// apart. The header is then made to lie about the size, which is the only
	// thing on trial.
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			src.SetRGBA(x, y, imgcolor.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, nil); err != nil {
		t.Fatal(err)
	}
	// The control: with its own size it draws, so a blank page below is the
	// refusal and not the decoder giving up.
	drew := jpegPage(t, jb.Bytes(), nil)
	if control, err := Page(drew, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	} else if inked(control) == 0 {
		t.Fatal("the JPEG does not draw even at its own size, so the test says nothing")
	}

	was := jpegSize
	jpegSize = func([]byte) (int, int) { return 9449, 13701 }
	defer func() { jpegSize = was }()
	d := jpegPage(t, jb.Bytes(), nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from a JPEG claiming 129.5 megapixels, which is "+
			"past the bound a JPEG has always had", ink)
	}
}
