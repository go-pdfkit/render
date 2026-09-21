package render

import (
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
	jpxSize = func([]byte) (int, int) { return 4, 4 }
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
	jpxSize = func([]byte) (int, int) { return 4, 4 }
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
