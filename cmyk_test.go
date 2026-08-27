package render

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/go-pdfkit/reader"
)

// asCMYK makes the JPEG decoder hand back a four-component image of one
// colour, which is what Go returns for an Adobe CMYK JPEG. Go's encoder writes
// three components whatever it is given, so the four-component case cannot be
// built by encoding one; jpegDecode is a variable precisely so a test can put
// something else behind it.
func asCMYK(t *testing.T, c color.CMYK) func() {
	t.Helper()
	was := jpegDecode
	jpegDecode = func([]byte) (image.Image, error) {
		src := image.NewCMYK(image.Rect(0, 0, 8, 8))
		for i := 0; i+3 < len(src.Pix); i += 4 {
			src.Pix[i], src.Pix[i+1] = c.C, c.M
			src.Pix[i+2], src.Pix[i+3] = c.Y, c.K
		}
		return src, nil
	}
	return func() { jpegDecode = was }
}

// jpegPage draws one JPEG over the whole of a small page.
func jpegPage(t *testing.T, data []byte, extra reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	dict := reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"ColorSpace": reader.Name("DeviceCMYK"), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode")}
	for k, v := range extra {
		dict[k] = v
	}
	img := w.Add(&reader.Stream{Dict: dict, Raw: data})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 8, 8),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 8 0 0 8 0 0 cm /I Do Q")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
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

func TestAnAdobeCMYKJPEGIsTurnedOver(t *testing.T) {
	// A CMYK JPEG stores its ink inverted. Samples of no ink at all therefore
	// mean full ink, and a page drawn from them without turning them over is
	// solid black — which is what 23 of the corpus's 35 CMYK-JPEG files were.
	//
	// The image here is written as "no ink", so a reader that turns it over
	// draws black and one that does not draws white. The direction is what
	// matters, and it is checked in both configurations below.
	defer asCMYK(t, color.CMYK{})()
	d := jpegPage(t, []byte("stands in for a fax of a JPEG"), nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(img, 4, 4) {
		t.Errorf("a CMYK JPEG of zero samples drew %s, want the ink turned over", pixel(img, 4, 4))
	}
}

func TestADecodeArrayTurnsTheCMYKJPEGBackAgain(t *testing.T) {
	// /Decode [1 0 1 0 1 0 1 0] asks for the samples backwards, which for a
	// CMYK JPEG cancels the inversion above. Eleven DVLA forms are written
	// that way, and they are drawn correctly today only because both halves
	// were missing at once — so a fix to either half alone breaks them.
	defer asCMYK(t, color.CMYK{})()
	d := jpegPage(t, []byte("stands in for a JPEG"), reader.Dict{"Decode": nums(1, 0, 1, 0, 1, 0, 1, 0)})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 4, 4) {
		t.Errorf("with an inverting /Decode the page drew %s, want the two inversions to cancel",
			pixel(img, 4, 4))
	}
}

func TestOnlyAWhollyInvertingDecodeCounts(t *testing.T) {
	// Anything that is not [1 0] repeated is not this case and must not be
	// read as it: a decode array of the usual shape, one of odd length, one
	// with a value that is neither, and none at all.
	defer asCMYK(t, color.CMYK{})()
	r := &renderer{doc: jpegPage(t, []byte("stands in for a JPEG"), nil)}
	for _, tc := range []struct {
		name string
		dict reader.Dict
		want bool
	}{
		{"none", reader.Dict{}, false},
		{"the identity", reader.Dict{"Decode": nums(0, 1, 0, 1, 0, 1, 0, 1)}, false},
		{"inverting", reader.Dict{"Decode": nums(1, 0, 1, 0, 1, 0, 1, 0)}, true},
		{"one pair inverting", reader.Dict{"Decode": nums(1, 0)}, true},
		{"a mixture", reader.Dict{"Decode": nums(1, 0, 0, 1)}, false},
		{"an odd number of values", reader.Dict{"Decode": nums(1, 0, 1)}, false},
		{"a single value", reader.Dict{"Decode": nums(1)}, false},
		{"not an array", reader.Dict{"Decode": reader.Integer(1)}, false},
		{"values that are not numbers", reader.Dict{"Decode": reader.Array{
			reader.Name("one"), reader.Name("zero")}}, false},
	} {
		if got := r.decodeInverts(tc.dict); got != tc.want {
			t.Errorf("%s: decodeInverts = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAGreyJPEGIsLeftAlone(t *testing.T) {
	// Only a four-component JPEG is turned over. A grey one comes back from
	// the decoder as *image.Gray and is drawn as it stands.
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	img := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode")}, Raw: buf.Bytes()})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 8, 8),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 8 0 0 8 0 0 cm /I Do Q")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(pic, 4, 4) {
		t.Errorf("a black grey-scale JPEG drew %s", pixel(pic, 4, 4))
	}
}
