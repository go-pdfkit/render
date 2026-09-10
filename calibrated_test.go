package render

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/go-pdfkit/reader"
)

// calSpace builds a colour space from an array written the way a file writes
// it, so these tests exercise the reading as well as the arithmetic.
func calSpace(t *testing.T, arr reader.Array) *space {
	t.Helper()
	r := &renderer{}
	return r.colourSpaceArray(arr[0].(reader.Name), arr, nil, 0)
}

// d65 and adobeRGBUnderD50 are the two spaces the tests below use. The second
// is the one openpdf's PDF 2.0 test file carries.
var (
	d65              = nums(0.9505, 1.0, 1.0890)
	adobeMatrix      = nums(0.7161, 0.2582, 0.0000, 0.1009, 0.7249, 0.0518, 0.1472, 0.0168, 0.7734)
	adobeWhiteD50    = nums(0.9643, 1.0000, 0.8251)
	gammaTwoPointTwo = nums(2.2, 2.2, 2.2)
)

func TestCalGrayIsNotDeviceGray(t *testing.T) {
	// The defect this file exists for. With the gamma the format defaults to,
	// a CalGray component is linear luminance, so a mid grey is far lighter
	// than the same number read as a device grey. Reading it as DeviceGray
	// was worth 60 levels.
	s := calSpace(t, reader.Array{reader.Name("CalGray"), reader.Dict{"WhitePoint": d65}})
	if s.name != "CalGray" {
		t.Fatalf("space = %q, want CalGray", s.name)
	}
	got := s.convert([]float64{0.5})
	if got.R != 188 || got.G != 188 || got.B != 188 {
		t.Errorf("mid CalGray = %v, want 188 on each channel", got)
	}
	if deviceGray.convert([]float64{0.5}).R == got.R {
		t.Error("CalGray and DeviceGray agree at the mid tone; the gamma was dropped")
	}
}

func TestCalGrayHonoursItsGamma(t *testing.T) {
	// With gamma 2.2 the component is already close to an sRGB encoding, so
	// the mid tone comes back near where it went in. That the two gammas give
	// different answers is the whole point of reading the entry.
	s := calSpace(t, reader.Array{reader.Name("CalGray"),
		reader.Dict{"WhitePoint": d65, "Gamma": reader.Real(2.2)}})
	got := s.convert([]float64{0.5})
	if got.R < 120 || got.R > 136 {
		t.Errorf("mid CalGray at gamma 2.2 = %d, want it near the sRGB mid tone", got.R)
	}
}

func TestCalRGBReadsItsMatrixAndGamma(t *testing.T) {
	s := calSpace(t, reader.Array{reader.Name("CalRGB"),
		reader.Dict{"WhitePoint": adobeWhiteD50, "Gamma": gammaTwoPointTwo, "Matrix": adobeMatrix}})
	if s.name != "CalRGB" || s.components != 3 {
		t.Fatalf("space = %q with %d components, want CalRGB with 3", s.name, s.components)
	}
	// A saturated red in Adobe RGB is outside sRGB and comes back clipped,
	// but a NEUTRAL must stay neutral: the white point is the whole reason
	// the adaptation is there.
	white := s.convert([]float64{1, 1, 1})
	if white.R != 255 || white.G != 255 || white.B != 255 {
		t.Errorf("CalRGB white = %v, want 255 on each channel", white)
	}
	black := s.convert([]float64{0, 0, 0})
	if black.R != 0 || black.G != 0 || black.B != 0 {
		t.Errorf("CalRGB black = %v, want 0 on each channel", black)
	}
	// And a mid grey must not acquire a cast of more than a level or two.
	mid := s.convert([]float64{0.5, 0.5, 0.5})
	if d := int(mid.R) - int(mid.B); d > 2 || d < -2 {
		t.Errorf("CalRGB mid grey = %v, want it near neutral", mid)
	}
}

func TestCalRGBDiffersFromDeviceRGBOnTheRealDocument(t *testing.T) {
	// openpdf-core/pdf-2-0_PDF_2.0_image_with_BPC.pdf draws the SAME JPEG
	// stream twice, once through this space and once through DeviceRGB, and
	// its own page text says the two should differ. Reading CalRGB as
	// DeviceRGB made them identical.
	s := calSpace(t, reader.Array{reader.Name("CalRGB"),
		reader.Dict{"WhitePoint": adobeWhiteD50, "Gamma": gammaTwoPointTwo, "Matrix": adobeMatrix}})
	worst := 0
	for i := 0; i <= 16; i++ {
		for j := 0; j <= 16; j++ {
			for k := 0; k <= 16; k++ {
				v := []float64{float64(i) / 16, float64(j) / 16, float64(k) / 16}
				a, b := s.convert(v), deviceRGB.convert(v)
				for _, d := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}} {
					e := int(d[0]) - int(d[1])
					if e < 0 {
						e = -e
					}
					if e > worst {
						worst = e
					}
				}
			}
		}
	}
	// The extracted pictures differ by 110 at their peak; over the whole cube
	// the gap is at least that.
	if worst < 100 {
		t.Errorf("worst gap between CalRGB and DeviceRGB = %d levels, want at least 100", worst)
	}
	t.Logf("worst gap over the colour cube: %d levels", worst)
}

func TestACalibratedSpaceWithoutAWhitePointFallsBackToItsDeviceNamesake(t *testing.T) {
	// The format requires /WhitePoint. A file that omits it has said nothing
	// about its colours, so inventing an illuminant would be worse than
	// reading the numbers as the device space they resemble.
	for _, tc := range []struct {
		name reader.Name
		want *space
		args []float64
	}{
		{"CalGray", deviceGray, []float64{0.5}},
		{"CalRGB", deviceRGB, []float64{0.5, 0.5, 0.5}},
	} {
		t.Run(string(tc.name), func(t *testing.T) {
			for label, d := range map[string]reader.Dict{
				"no dictionary":    nil,
				"empty dictionary": reader.Dict{},
				"a partial point":  reader.Dict{"WhitePoint": nums(0.95, 1.0)},
				"a zero luminance": reader.Dict{"WhitePoint": nums(0.95, 0, 1.09)},
				"a point of names": reader.Dict{"WhitePoint": reader.Array{reader.Name("x"), reader.Name("y"), reader.Name("z")}},
			} {
				got := calSpace(t, reader.Array{tc.name, d})
				if got.convert(tc.args) != tc.want.convert(tc.args) {
					t.Errorf("%s: %s did not fall back to %s", label, tc.name, tc.want.name)
				}
			}
		})
	}
}

func TestACalibratedSpaceIgnoresAMalformedGammaOrMatrix(t *testing.T) {
	// A gamma of zero or a matrix of the wrong length is not a space with an
	// odd gamma, it is a file that got it wrong. The defaults stand, and the
	// white point still applies.
	base := calSpace(t, reader.Array{reader.Name("CalRGB"), reader.Dict{"WhitePoint": d65}})
	for label, d := range map[string]reader.Dict{
		"a gamma of zero":   reader.Dict{"WhitePoint": d65, "Gamma": nums(0, 2.2, 2.2)},
		"a short gamma":     reader.Dict{"WhitePoint": d65, "Gamma": nums(2.2, 2.2)},
		"a matrix of eight": reader.Dict{"WhitePoint": d65, "Matrix": nums(1, 0, 0, 0, 1, 0, 0, 0)},
		"a matrix of numbers and a name": reader.Dict{"WhitePoint": d65,
			"Matrix": reader.Array{reader.Real(1), reader.Real(0), reader.Real(0), reader.Real(0),
				reader.Real(1), reader.Real(0), reader.Real(0), reader.Real(0), reader.Name("one")}},
	} {
		got := calSpace(t, reader.Array{reader.Name("CalRGB"), d})
		v := []float64{0.25, 0.5, 0.75}
		if got.convert(v) != base.convert(v) {
			t.Errorf("%s: the defaults were not kept (%v vs %v)", label, got.convert(v), base.convert(v))
		}
	}
	if got := calSpace(t, reader.Array{reader.Name("CalGray"),
		reader.Dict{"WhitePoint": d65, "Gamma": reader.Real(0)}}).convert([]float64{0.5}); got != (color.RGBA{R: 188, G: 188, B: 188, A: 255}) {
		t.Errorf("a CalGray gamma of zero was used: %v", got)
	}
}

// jpegInSpace draws one 8x8 JPEG of a single colour through the named colour
// space and returns the pixel it drew, so the tests below can say what the
// space did to the samples rather than what the dispatch thinks it did.
func jpegInSpace(t *testing.T, space reader.Object, src image.Image) color.RGBA {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	img := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"ColorSpace": space, "BitsPerComponent": reader.Integer(8),
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
	c := pic.At(4, 4)
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func TestAThreeComponentJPEGGoesThroughItsCalibratedSpace(t *testing.T) {
	// The defect that made the library unreachable. jpegThroughSpace asked the
	// colour space only for a ONE-component picture, so a three-component JPEG
	// in a CalRGB space was drawn as though the space were DeviceRGB and the
	// calibration never ran. On openpdf's PDF 2.0 test file that was worth a
	// peak of 110 levels against poppler; opening this path took it to 20.
	// A SATURATED colour, because that is where a calibrated space and its
	// device namesake part company. A neutral barely moves -- gamma 2.2
	// decoding and the sRGB encoding very nearly cancel, and the white-point
	// adaptation keeps a grey grey -- so a mid grey would pass this test
	// whether the space was consulted or not.
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 250, 113, 7, 255
	}
	cal := jpegInSpace(t, reader.Array{reader.Name("CalRGB"),
		reader.Dict{"WhitePoint": adobeWhiteD50, "Gamma": gammaTwoPointTwo, "Matrix": adobeMatrix}}, src)
	dev := jpegInSpace(t, reader.Name("DeviceRGB"), src)
	if cal == dev {
		t.Fatalf("a CalRGB JPEG was drawn exactly as a DeviceRGB one (%v); the space was never asked", cal)
	}
	worst := 0
	for _, d := range [][2]uint8{{cal.R, dev.R}, {cal.G, dev.G}, {cal.B, dev.B}} {
		e := int(d[0]) - int(d[1])
		if e < 0 {
			e = -e
		}
		if e > worst {
			worst = e
		}
	}
	if worst < 20 {
		t.Errorf("CalRGB = %v, DeviceRGB = %v, worst gap %d levels; want the calibration to show",
			cal, dev, worst)
	}
	t.Logf("CalRGB %v vs DeviceRGB %v: %d levels", cal, dev, worst)
}

func TestAGreyJPEGGoesThroughItsCalibratedSpace(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range src.Pix {
		src.Pix[i] = 128
	}
	cal := jpegInSpace(t, reader.Array{reader.Name("CalGray"),
		reader.Dict{"WhitePoint": d65}}, src)
	dev := jpegInSpace(t, reader.Name("DeviceGray"), src)
	if cal == dev {
		t.Fatalf("a CalGray JPEG was drawn exactly as a DeviceGray one (%v)", cal)
	}
	if cal.R < 180 {
		t.Errorf("CalGray mid tone = %v, want it near 188: the component is linear luminance", cal)
	}
}

func TestAJPEGWhoseSpaceDoesNotFitItsPlanesIsLeftAlone(t *testing.T) {
	// A three-component JPEG declared in a one-component space, and a grey one
	// declared in three: the sample count and the space disagree, so there is
	// no honest way to put the samples through it. The picture is drawn from
	// the decoder's own colours rather than dropped.
	rgb := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(rgb.Pix); i += 4 {
		rgb.Pix[i], rgb.Pix[i+1], rgb.Pix[i+2], rgb.Pix[i+3] = 200, 60, 60, 255
	}
	got := jpegInSpace(t, reader.Array{reader.Name("CalGray"), reader.Dict{"WhitePoint": d65}}, rgb)
	if got.A != 255 {
		t.Errorf("a mismatched picture was dropped: %v", got)
	}
	grey := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range grey.Pix {
		grey.Pix[i] = 128
	}
	got = jpegInSpace(t, reader.Array{reader.Name("CalRGB"),
		reader.Dict{"WhitePoint": d65, "Matrix": adobeMatrix}}, grey)
	if got.A != 255 {
		t.Errorf("a mismatched grey picture was dropped: %v", got)
	}
}

func TestACalibratedSpaceArrayWithNoDictionaryAtAll(t *testing.T) {
	// [/CalRGB] with nothing after it. calDict has no second element to read.
	for _, tc := range []struct {
		name reader.Name
		want *space
		args []float64
	}{
		{"CalGray", deviceGray, []float64{0.5}},
		{"CalRGB", deviceRGB, []float64{0.5, 0.5, 0.5}},
	} {
		got := calSpace(t, reader.Array{tc.name})
		if got.convert(tc.args) != tc.want.convert(tc.args) {
			t.Errorf("%s alone did not fall back to %s", tc.name, tc.want.name)
		}
	}
}
