package render

import (
	"github.com/go-gfx/gfx/raster"
	"image"
	"testing"
)

// TestAJPXPictureOfInkReachesColourThroughThePrintingPrimaries.
//
// go-images/jpeg2000 hands back an *image.CMYK for a picture whose own JP2
// header declares CMYK. raster.FromImage would then read it with the standard
// library's naive (1-c)(1-k), which is not the formula the rest of this
// package uses -- the same divergence the CMYK JPEG path had before it was
// closed. This is render's half: ask what the picture is.
func TestAJPXPictureOfInkReachesColourThroughThePrintingPrimaries(t *testing.T) {
	// Full black ink and nothing else. Through the printing primaries that is
	// the SWOP key, (35, 31, 32); through (1-c)(1-k) it would be absolute
	// black, which no ink on paper is.
	was := jpxDecode
	jpxDecode = func([]byte) (image.Image, error) {
		cm := image.NewCMYK(image.Rect(0, 0, 4, 4))
		for i := 0; i < len(cm.Pix); i += 4 {
			cm.Pix[i+3] = 255
		}
		return cm, nil
	}
	defer func() { jpxDecode = was }()
	wasSize := jpxSize
	jpxSize = func([]byte) (int, int, bool) { return 4, 4, false }
	defer func() { jpxSize = wasSize }()

	d := jpxPage(t, jpxImage(t, 4, 4), 4, 4)
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures came back, want 1", len(got))
	}
	pic := got[0].Pic
	if pic == nil {
		t.Fatal("the picture came back with no pixels")
	}
	i := (1*pic.W + 1) * 4
	gotRGB := [3]uint8{pic.Pix[i], pic.Pix[i+1], pic.Pix[i+2]}
	if gotRGB == [3]uint8{0, 0, 0} {
		t.Fatal("full key came out as absolute black; the naive formula ran")
	}
	if want := [3]uint8{35, 31, 32}; gotRGB != want {
		t.Errorf("full key = %v, want the SWOP key %v", gotRGB, want)
	}
}

// TestAJPXPictureOfColourIsStillDrawnAsColour: the CMYK branch must not
// swallow the ordinary case.
func TestAJPXPictureOfColourIsStillDrawnAsColour(t *testing.T) {
	was := jpxDecode
	jpxDecode = func([]byte) (image.Image, error) {
		im := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for i := 0; i < len(im.Pix); i += 4 {
			im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = 200, 60, 20, 255
		}
		return im, nil
	}
	defer func() { jpxDecode = was }()
	wasSize := jpxSize
	jpxSize = func([]byte) (int, int, bool) { return 4, 4, false }
	defer func() { jpxSize = wasSize }()

	d := jpxPage(t, jpxImage(t, 4, 4), 4, 4)
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures came back, want 1", len(got))
	}
	pic := got[0].Pic
	if pic == nil {
		t.Fatal("the picture came back with no pixels")
	}
	i := (1*pic.W + 1) * 4
	if v := [3]uint8{pic.Pix[i], pic.Pix[i+1], pic.Pix[i+2]}; v != [3]uint8{200, 60, 20} {
		t.Errorf("an RGB picture came out as %v, want its own colour", v)
	}
}

// TestAnOpaqueJPXPictureIsTakenNotConverted is the other half of the adoption:
// adopted() is tested directly, and this checks that decodeJPX asks it and that
// the answer it takes is the same picture raster.FromImage would have built.
//
// A JPEG 2000 codestream carries no alpha, so the decoder writes every pixel
// opaque, and at an alpha of 255 premultiplied and straight are the same bytes.
func TestAnOpaqueJPXPictureIsTakenNotConverted(t *testing.T) {
	made := func() *image.RGBA {
		im := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				i := im.PixOffset(x, y)
				im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] =
					uint8(10*x+1), uint8(20*y+2), uint8(30+x+y), 255
			}
		}
		return im
	}

	wasSize := jpxSize
	jpxSize = func([]byte) (int, int, bool) { return 4, 4, false }
	defer func() { jpxSize = wasSize }()
	was := jpxDecode
	defer func() { jpxDecode = was }()

	// What raster.FromImage would have produced, for comparison.
	want := raster.FromImage(made()).Pix

	jpxDecode = func([]byte) (image.Image, error) { return made(), nil }
	d := jpxPage(t, jpxImage(t, 4, 4), 4, 4)
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures came back, want 1", len(got))
	}
	if len(got[0].Pic.Pix) != len(want) {
		t.Fatalf("%d bytes, want %d", len(got[0].Pic.Pix), len(want))
	}
	for i := range want {
		if got[0].Pic.Pix[i] != want[i] {
			t.Fatalf("byte %d is %d, and converting gives %d", i, got[0].Pic.Pix[i], want[i])
		}
	}
}

// TestAJPXPictureThatCannotBeTakenIsConverted keeps the other side of the
// adoption alive. Every JPEG 2000 fixture in this package hands back a dense
// opaque *image.RGBA, so once adoption existed, nothing exercised the conversion
// it falls back to -- and a fallback no test reaches is a fallback nobody knows
// is broken.
func TestAJPXPictureThatCannotBeTakenIsConverted(t *testing.T) {
	wasSize := jpxSize
	jpxSize = func([]byte) (int, int, bool) { return 4, 4, false }
	defer func() { jpxSize = wasSize }()
	was := jpxDecode
	defer func() { jpxDecode = was }()

	// Translucent, so premultiplied is not straight and the bytes may not be
	// taken as they are.
	jpxDecode = func([]byte) (image.Image, error) {
		im := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for i := 0; i < len(im.Pix); i += 4 {
			// Premultiplied: half-covered mid grey.
			im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = 64, 64, 64, 128
		}
		return im, nil
	}
	d := jpxPage(t, jpxImage(t, 4, 4), 4, 4)
	got, err := Images(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d pictures came back, want 1", len(got))
	}
	// Un-premultiplied: 64 at an alpha of 128 is 127 or 128 straight, not 64.
	// The point is that it is NOT the premultiplied byte.
	if v := got[0].Pic.Pix[0]; v == 64 {
		t.Errorf("the premultiplied byte came through unconverted: %d", v)
	}
	if a := got[0].Pic.Pix[3]; a != 128 {
		t.Errorf("alpha came back as %d, want 128", a)
	}
}
