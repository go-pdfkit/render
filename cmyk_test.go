package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"

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

// TestACMYKJPEGIsConvertedLikeEveryOtherCMYK is the inconsistency this closes.
//
// A CMYK image written as SAMPLES went through cmykToRGBA and the printing
// primaries; the same values arriving as a JPEG went through raster.FromImage,
// which uses the standard library's naive formula. Two paths, two answers, for
// one set of numbers.
//
// Eight DVLA forms carry a YCCK scan of a whole page, and every one of them
// differed from poppler on 99% of its pixels for that reason alone. Their mean
// squared error fell by about an order of magnitude when this closed: v112 from
// 76.1 to 3.9, v888 from 79.4 to 5.7, v317 from 192.1 to 26.0.
func TestACMYKJPEGIsConvertedLikeEveryOtherCMYK(t *testing.T) {
	// Cyan and magenta at full strength, which prints a violet-blue rather than
	// the primary blue. An Adobe JPEG stores its ink turned over, so the
	// samples written here are the complements of that ink.
	defer asCMYK(t, color.CMYK{C: 0, M: 0, Y: 255, K: 255})()
	d := jpegPage(t, []byte("stands in for a JPEG"), nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 4, 4, color.RGBA{46, 49, 146, 255}, 3)

	// The same four numbers written as samples have to land in the same place,
	// which is the whole of the point.
	d2 := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceCMYK"),
	}, []byte{255, 255, 0, 0}, "")
	wantColour(t, draw(t, d2, Options{}), 10, 10, color.RGBA{46, 49, 146, 255}, 3)
}

// asCMYKImage puts a four-component image of the caller's making behind the JPEG
// decoder. Go's encoder writes three components whatever it is given, so a
// four-component picture cannot be built by encoding one.
func asCMYKImage(t *testing.T, side int, fill func(x, y int) color.CMYK) func() {
	t.Helper()
	was := jpegDecode
	jpegDecode = func([]byte) (image.Image, error) {
		src := image.NewCMYK(image.Rect(0, 0, side, side))
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				c := fill(x, y)
				i := src.PixOffset(x, y)
				src.Pix[i], src.Pix[i+1] = c.C, c.M
				src.Pix[i+2], src.Pix[i+3] = c.Y, c.K
			}
		}
		return src, nil
	}
	return func() { jpegDecode = was }
}

// bigCMYKPage draws one side-by-side CMYK JPEG over a page of that size.
func bigCMYKPage(t *testing.T, side int) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	img := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(side), "Height": reader.Integer(side),
		"ColorSpace": reader.Name("DeviceCMYK"), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode")}, Raw: []byte{0xFF, 0xD8}})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, float64(side), float64(side)),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte(fmt.Sprintf("q %d 0 0 %d 0 0 cm /I Do Q", side, side))})})
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

// TestACMYKPictureIsConvertedOncePerDistinctQuad.
//
// CMYKToSRGBWebCoated was called once per PIXEL. On a DVLA form -- a 2480 by 3508
// CMYK scan, 8 699 840 pixels -- that was 59% of the page, and the picture carries
// 48 370 distinct quads: 0.56%. Cached, the page went from 893 ms to 210 ms.
//
// The bound is a ratio against a witness the cache cannot help: the SAME picture
// with every pixel a different quad. Both convert the same number of pixels; only
// one of them can answer from the table. A duration would pin this machine and a
// call count would need a hook inside a third-party colour package.
func TestACMYKPictureIsConvertedOncePerDistinctQuad(t *testing.T) {
	const side = 360 // 129 600 pixels
	best := func(fill func(x, y int) color.CMYK) time.Duration {
		defer asCMYKImage(t, side, fill)()
		d := bigCMYKPage(t, side)
		draw(t, d, Options{}) // warm
		out := time.Duration(1 << 62)
		for i := 0; i < 3; i++ {
			start := time.Now()
			draw(t, d, Options{})
			if took := time.Since(start); took < out {
				out = took
			}
		}
		return out
	}
	// Few quads, which is what a scanned form is.
	subject := best(func(x, y int) color.CMYK {
		v := uint8((x/40 + y/40) % 4 * 60)
		return color.CMYK{C: v, M: v, Y: v, K: v}
	})
	// Every pixel its own quad, which the cache can do nothing with.
	witness := best(func(x, y int) color.CMYK {
		n := y*side + x
		return color.CMYK{C: uint8(n), M: uint8(n >> 8), Y: uint8(n >> 16), K: uint8(n * 7)}
	})
	if subject > witness/2 {
		t.Errorf("a four-quad picture took %v against an all-distinct one's %v (%.2f); "+
			"the conversion is being asked per pixel rather than per distinct quad",
			subject, witness, float64(subject)/float64(witness))
	}
}

// TestACMYKPictureAndAnRGBOneOnThePageDoNotShareAnswers.
//
// One table serves both, and their keys share a space: the quad (0, A, B, C) packs
// into exactly the bits the triple (A, B, C) uses. Without a generation between the
// pictures the second reads the first's answers -- and it renders, in the wrong
// colours, only where the keys happen to meet, which is the kind of wrong that no
// smoke test and no crash reports.
//
// BOTH ORDERS, because each picture starts its own generation and a test of one
// order only exercises the other picture's call. With CMYK first, the RGB path's
// nextImage protects it; removing the CMYK path's own call changes nothing and the
// mutation survives. It is RGB FIRST that the CMYK picture's own call defends.
//
// The decoder seam hands back the pictures in turn, because Go's encoder cannot
// write a four-component JPEG.
func TestACMYKPictureAndAnRGBOneOnThePageDoNotShareAnswers(t *testing.T) {
	for _, cmykFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "CMYK first", false: "RGB first"}[cmykFirst], func(t *testing.T) {
			twoPicturesShareNoAnswers(t, cmykFirst)
		})
	}
}

func twoPicturesShareNoAnswers(t *testing.T, cmykFirst bool) {
	t.Helper()
	const a, b, c = 0x30, 0x60, 0x90
	was := jpegDecode
	t.Cleanup(func() { jpegDecode = was })
	cmyk := func() (image.Image, error) {
		// Cyan zero, so this quad's key is exactly the RGB triple's.
		src := image.NewCMYK(image.Rect(0, 0, 4, 4))
		for i := 0; i+3 < len(src.Pix); i += 4 {
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 0, a, b, c
		}
		return src, nil
	}
	rgb := func() (image.Image, error) {
		src := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for i := 0; i+3 < len(src.Pix); i += 4 {
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = a, b, c, 255
		}
		return src, nil
	}
	calls := 0
	jpegDecode = func([]byte) (image.Image, error) {
		calls++
		if (calls == 1) == cmykFirst {
			return cmyk()
		}
		return rgb()
	}

	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	mk := func(space reader.Object) reader.Object {
		return w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(4), "Height": reader.Integer(4),
			"ColorSpace": space, "BitsPerComponent": reader.Integer(8),
			"Filter": reader.Name("DCTDecode")}, Raw: []byte{0xFF, 0xD8}})
	}
	cmykRef := mk(reader.Name("DeviceCMYK"))
	rgbRef := mk(reader.Array{reader.Name("CalRGB"), reader.Dict{
		"WhitePoint": nums(0.9505, 1, 1.089), "Gamma": nums(2.2, 2.2, 2.2)}})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 8, 4),
		"Resources": reader.Dict{"XObject": reader.Dict{"C": cmykRef, "R": rgbRef}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte(map[bool]string{
				true:  "q 4 0 0 4 0 0 cm /C Do Q q 4 0 0 4 4 0 cm /R Do Q",
				false: "q 4 0 0 4 0 0 cm /R Do Q q 4 0 0 4 4 0 cm /C Do Q",
			}[cmykFirst])})})
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
	got := draw(t, d, Options{})

	// What each picture gives with nothing before it in the table.
	jpegDecode = func([]byte) (image.Image, error) { return rgb() }
	rgbAlone := draw(t, jpegPage(t, []byte{0xFF, 0xD8}, reader.Dict{
		"ColorSpace": reader.Array{reader.Name("CalRGB"), reader.Dict{
			"WhitePoint": nums(0.9505, 1, 1.089), "Gamma": nums(2.2, 2.2, 2.2)}}}), Options{})
	jpegDecode = func([]byte) (image.Image, error) { return cmyk() }
	cmykAlone := draw(t, jpegPage(t, []byte{0xFF, 0xD8}, nil), Options{})

	// Whichever went second is the one that could have read the other's answers.
	secondX := 5
	want, which := rgbAlone.At(1, 1), "RGB"
	other := "CMYK"
	if !cmykFirst {
		want, which, other = cmykAlone.At(1, 1), "CMYK", "RGB"
	}
	if p := got.At(secondX, 1); p.R != want.R || p.G != want.G || p.B != want.B {
		t.Errorf("the %s picture drew %d,%d,%d after a %s one and %d,%d,%d alone; "+
			"it read the %s answer", which, p.R, p.G, p.B, other, want.R, want.G, want.B, other)
	}
}
