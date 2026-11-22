package render

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

// embeddedOverWhite lays every picture an SVG embeds for what shapes cannot say
// (the <image> elements of the body, not the ones in <defs>) over white, at the
// pixels it names. For a page made only of such pictures that is the page, and
// it can be compared with what the rasteriser drew.
func embeddedOverWhite(t *testing.T, doc []byte, w, h int) *raster.Image {
	t.Helper()
	out := raster.New(w, h)
	fill(out, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	dec := xml.NewDecoder(bytes.NewReader(doc))
	inDefs := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			if tk.Name.Local == "defs" {
				inDefs = true
			}
			if tk.Name.Local != "image" || inDefs {
				continue
			}
			var x, y int
			var href string
			for _, a := range tk.Attr {
				switch a.Name.Local {
				case "x":
					x = atoi(t, a.Value)
				case "y":
					y = atoi(t, a.Value)
				case "href":
					href = a.Value
				}
			}
			raw, err := base64.StdEncoding.DecodeString(href[strings.Index(href, ",")+1:])
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			b := img.Bounds()
			for j := 0; j < b.Dy(); j++ {
				for i := 0; i < b.Dx(); i++ {
					c := color.NRGBAModel.Convert(img.At(b.Min.X+i, b.Min.Y+j)).(color.NRGBA)
					under := out.At(x+i, y+j)
					out.Set(x+i, y+j, blend(under, color.RGBA{R: c.R, G: c.G, B: c.B, A: 255}, float64(c.A)/255))
				}
			}
		case xml.EndElement:
			if tk.Name.Local == "defs" {
				inDefs = false
			}
		}
	}
	return out
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a whole number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// maxDifference is the largest difference in any channel of any pixel.
func maxDifference(a, b *raster.Image) int {
	worst := 0
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		worst = max(worst, d)
	}
	return worst
}

// A page whose every mark is one the rasteriser must draw — a pattern, a
// shading, a soft mask, a group under a mask — comes out as pictures, and those
// pictures laid on the page are what the rasteriser drew.
func TestWhatShapesCannotSayIsEmbeddedAsPixels(t *testing.T) {
	ramp := func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
			"Type": reader.Name("Pattern"), "PatternType": reader.Integer(2),
			"Shading": reader.Dict{
				"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
				"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
				"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
			},
		})}}
	}
	tile := func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("Pattern"), "PatternType": reader.Integer(1), "PaintType": reader.Integer(1),
			"TilingType": reader.Integer(1), "BBox": nums(0, 0, 10, 10), "XStep": reader.Integer(10),
			"YStep": reader.Integer(10), "Resources": reader.Dict{},
		}, Raw: []byte("1 0 0 rg 0 0 5 5 re f 0 0 1 rg 5 5 5 5 re f")})}}
	}
	mask := func(content string) func(w *reader.Writer) reader.Dict {
		return func(w *reader.Writer) reader.Dict {
			form := maskForm(w, "/S1 sh", reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}, nil)
			group := w.Add(&reader.Stream{Dict: reader.Dict{
				"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"), "BBox": nums(0, 0, 100, 100),
				"Group": reader.Dict{"S": reader.Name("Transparency")},
			}, Raw: []byte("0 g 10 10 50 50 re f 30 10 50 50 re f")})
			return reader.Dict{
				"XObject": reader.Dict{"F1": group},
				"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"Type": reader.Name("ExtGState"),
					"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form}}),
					"GS1": w.Add(reader.Dict{"ca": nums(0.5)[0], "SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form}})},
			}
		}
	}
	cases := []struct {
		name    string
		content string
		build   func(w *reader.Writer) reader.Dict
	}{
		{"shading pattern fill", "/Pattern cs /P1 scn 10 10 80 80 re f", ramp},
		{"shading pattern stroke", "/Pattern CS /P1 SCN 8 w 10 50 m 90 50 l S", ramp},
		{"tiling pattern fill", "/Pattern cs /P1 scn 10 10 80 80 re f", tile},
		{"the sh operator", "/P1 sh", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Shading": reader.Dict{"P1": greyRamp(w)}}
		}},
		{"a soft mask on a fill", "/GS0 gs 0 0 100 100 re f", mask("")},
		{"a soft mask on a stroke", "/GS0 gs 10 w 0 50 m 100 50 l S", mask("")},
		{"a group under a soft mask", "/GS0 gs /F1 Do", mask("")},
		{"a faded group under a soft mask", "/GS1 gs /F1 Do", mask("")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := shadedPage(t, c.content, c.build)
			doc := svgOf(t, d, SVGOptions{RasterScale: 1})
			if !bytes.Contains(doc, []byte("<image")) {
				t.Fatal("nothing was embedded")
			}
			want, err := Page(d, 1, Options{Scale: 1})
			if err != nil {
				t.Fatal(err)
			}
			got := embeddedOverWhite(t, doc, want.W, want.H)
			// Alpha is recovered from two renderings and stored in eight bits.
			if diff := maxDifference(got, want); diff > 3 {
				t.Errorf("the embedded pictures differ from the raster by up to %d levels", diff)
			} else {
				t.Logf("%s: worst channel difference %d", c.name, diff)
			}
		})
	}
}

func TestAMarkThatTheMaskHidesWritesNothing(t *testing.T) {
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "0 g 0 0 100 100 re f", nil, nil)
		return reader.Dict{"S": reader.Name("Luminosity"), "G": form}
	}, "")
	if doc := string(svgOf(t, d, SVGOptions{})); strings.Contains(doc, "<image") {
		t.Error("a mark nothing of which shows was written")
	}
}

func TestVectorMarksAreShapes(t *testing.T) {
	content := "1 0 0 rg 10 10 30 30 re f 0 0 1 RG 4 w 1 J 1 j [6 3] 2 d 10 50 m 40 80 70 20 90 60 c S " +
		"0 1 0 rg 50 10 m 90 10 l 70 40 l h 60 20 m 80 20 l 70 35 l h f* " +
		"/GS0 gs 0 g 0 0 5 5 re f"
	d := shadedPage(t, content, func(w *reader.Writer) reader.Dict {
		return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"ca": nums(0.5)[0], "CA": nums(0.25)[0]})}}
	})
	doc := string(svgOf(t, d, SVGOptions{}))
	for _, want := range []string{`fill="#ff0000"`, `stroke="#0000ff"`, `stroke-linecap="round"`,
		`stroke-linejoin="round"`, `stroke-dasharray="12 6"`, `fill-rule="evenodd"`, `fill-opacity="0.5"`, `C `} {
		if !strings.Contains(doc, want) {
			t.Errorf("the output lacks %s", want)
		}
	}
	if strings.Contains(doc, "<image") {
		t.Error("a vector page embedded a picture")
	}
}

func TestStrokeStylesAreWritten(t *testing.T) {
	cases := map[string]string{
		"2 J 0 j 10 w 10 10 m 50 50 l S":             `stroke-linecap="square"`,
		"2 j 10 w 10 10 m 50 50 l 90 10 l S":         `stroke-linejoin="bevel"`,
		"2 M 10 w 10 10 m 50 50 l 90 10 l S":         `stroke-miterlimit="2"`,
		"[3] 1 d 10 w 10 10 m 50 50 l S":             `stroke-dashoffset="2"`,
		"[3 -1] 0 d 10 w 10 10 m 50 50 l S":          `stroke-width="`,
		"[0 0] 0 d 10 w 10 10 m 50 50 l S":           `stroke-width="`,
		"/GS0 gs 10 w 10 10 m 50 50 l S":             `stroke-opacity="0.25"`,
		"/GS0 gs 10 10 m 50 50 l S 0 0 10 10 re W n": `<clipPath`,
	}
	for content, want := range cases {
		d := shadedPage(t, content, func(w *reader.Writer) reader.Dict {
			return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"CA": nums(0.25)[0]})}}
		})
		if doc := string(svgOf(t, d, SVGOptions{})); !strings.Contains(doc, want) {
			t.Errorf("%q: the output lacks %s", content, want)
		}
	}
	// A dash of nothing is a solid line, not an invalid attribute.
	d := shadedPage(t, "[0 0] 0 d 10 w 10 10 m 50 50 l S", func(w *reader.Writer) reader.Dict { return reader.Dict{} })
	if doc := string(svgOf(t, d, SVGOptions{})); strings.Contains(doc, "dasharray") {
		t.Error("a dash of nothing was written")
	}
}

func TestClipsNestAndApplyToWhatFollows(t *testing.T) {
	d := shadedPage(t, "q 10 10 80 80 re W n 20 20 m 80 20 l 50 80 l h W* n 0 g 0 0 100 100 re f Q 0 0 5 5 re f",
		func(w *reader.Writer) reader.Dict { return reader.Dict{} })
	doc := string(svgOf(t, d, SVGOptions{}))
	if strings.Count(doc, "<clipPath") != 2 {
		t.Errorf("want two clips, got %d", strings.Count(doc, "<clipPath"))
	}
	if !strings.Contains(doc, `clip-rule="evenodd"`) {
		t.Error("the even-odd clip lost its rule")
	}
	// The second clip nests the first, and the fill after Q is outside both.
	if strings.Count(doc, `<g clip-path=`) != 1 {
		t.Errorf("want one clipped mark, got %d", strings.Count(doc, `<g clip-path=`))
	}
}

func TestTextIsWrittenOnceAsAnOutlineAndUsedForEveryLetter(t *testing.T) {
	d := pageWithFont(t, "BT /F 20 Tf 10 20 Td (HHH) Tj ET BT /F 20 Tf 1 Tr 10 40 Td (H) Tj ET", nil)
	doc := string(svgOf(t, d, SVGOptions{}))
	if n := strings.Count(doc, `<path id="g`); n != 1 {
		t.Errorf("want one glyph outline, got %d", n)
	}
	if n := strings.Count(doc, `<use `); n != 3 {
		t.Errorf("want three letters, got %d", n)
	}
	if !strings.Contains(doc, `stroke="#000000"`) {
		t.Error("the stroked letter was not stroked")
	}
	if strings.Contains(doc, "<text") || strings.Contains(doc, "font") {
		t.Error("text was written as text")
	}
}

func TestAnImageIsEmbeddedOnceWhateverTheNumberOfUses(t *testing.T) {
	// Opaque: a JPEG. A stencil mask has holes: a PNG.
	opaque := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(2),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceRGB"),
	}, []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0}, "q 10 0 0 10 0 0 cm /I Do Q q 10 0 0 10 10 10 cm /I Do Q")
	doc := string(svgOf(t, opaque, SVGOptions{}))
	if n := strings.Count(doc, `<image id=`); n != 1 {
		t.Errorf("want the picture once, got %d", n)
	}
	if n := strings.Count(doc, `<use href="#i`); n != 2 {
		t.Errorf("want two uses, got %d", n)
	}
	if !strings.Contains(doc, "data:image/jpeg;base64,") {
		t.Error("an opaque picture was not a JPEG")
	}
	stencil := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(2), "ImageMask": reader.Bool(true),
		"BitsPerComponent": reader.Integer(1),
	}, []byte{0x40, 0x80}, "1 0 0 rg 0 0 20 20 re W n q 20 0 0 20 0 0 cm /I Do Q")
	doc = string(svgOf(t, stencil, SVGOptions{}))
	if !strings.Contains(doc, "data:image/png;base64,") {
		t.Error("a picture with holes was not a PNG")
	}
	// Flattened to nothing: nothing to draw.
	flat := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
	}, []byte{0}, "q 0 0 0 0 0 0 cm /I Do Q")
	if doc := string(svgOf(t, flat, SVGOptions{})); strings.Contains(doc, "<image") {
		t.Error("a picture squashed to nothing was written")
	}
}

func TestAGroupThatIsFadedIsOneElementWithOneOpacity(t *testing.T) {
	d := shadedPage(t, "/GS0 gs /F1 Do", func(w *reader.Writer) reader.Dict {
		form := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":  nums(0, 0, 100, 100),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 10 10 50 50 re f 30 10 50 50 re f")})
		return reader.Dict{
			"XObject":   reader.Dict{"F1": form},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"ca": nums(0.5)[0]})},
		}
	})
	doc := string(svgOf(t, d, SVGOptions{}))
	if !strings.Contains(doc, `<g opacity="0.5">`) || strings.Contains(doc, "fill-opacity") {
		t.Errorf("the group was not one faded element: %s", doc)
	}
}

func TestMarksOutsideThePageAndNonsenseCoordinatesAreSafe(t *testing.T) {
	d := shadedPage(t, "1e38 1e38 m -1e38 1e38 l 5 5 l h f 1e38 w 0 0 m 1e38 1e38 l S "+
		"/Pattern cs /P1 scn 1e30 1e30 5 5 re f 1e38 0 0 1e38 0 0 cm 0 0 1 1 re f",
		func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
				"Type": reader.Name("Pattern"), "PatternType": reader.Integer(2),
				"Shading": reader.Dict{
					"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
					"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
				},
			})}}
		})
	svgOf(t, d, SVGOptions{})
}

func TestTheDocumentsOwnSizeIsInPointsAndItsViewBoxInPixels(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 30, 20}, "0 g 0 0 5 5 re f", nil)
	doc := string(svgOf(t, d, SVGOptions{RasterScale: 3}))
	if !strings.Contains(doc, `width="30" height="20" viewBox="0 0 90 60"`) {
		t.Errorf("unexpected header: %.120s", doc)
	}
	// A scale that is not a number is the default.
	for _, s := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if doc := string(svgOf(t, d, SVGOptions{RasterScale: s})); !strings.Contains(doc, `viewBox="0 0 60 40"`) {
			t.Errorf("scale %v: unexpected header: %.120s", s, doc)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestPageSVGRefusals(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 30, 20}, "0 g 0 0 5 5 re f 0 0 6 6 re f 0 0 7 7 re f", nil)
	var buf bytes.Buffer
	if err := PageSVG(d, 9, &buf, SVGOptions{}); err == nil {
		t.Error("a page that is not there was written")
	}
	if err := PageSVG(d, 1, failingWriter{}, SVGOptions{}); err == nil || err.Error() != "disk full" {
		t.Errorf("a write failure was not returned: %v", err)
	}
	if err := PageSVG(d, 1, &buf, SVGOptions{MaxBytes: 100}); !errors.Is(err, ErrSVGTooLarge) {
		t.Errorf("want ErrSVGTooLarge, got %v", err)
	}
	if buf.Len() != 0 {
		t.Error("something was written before the refusal")
	}
	// A page too large to be drawn at the scale asked for.
	big := onePage(t, [4]float64{0, 0, 20000, 20000}, "0 g 0 0 5 5 re f", nil)
	if err := PageSVG(big, 1, &buf, SVGOptions{}); err == nil {
		t.Error("a page past the pixel limit was written")
	}
}

// A single mark can be larger than the limit on its own: the limit is checked
// when the document is done as well as while it is written.
func TestTheLimitHoldsForOneLargeMark(t *testing.T) {
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(64), "Height": reader.Integer(64),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
	}, bytes.Repeat([]byte{0, 255, 17, 200}, 1024), "")
	var buf bytes.Buffer
	if err := PageSVG(d, 1, &buf, SVGOptions{MaxBytes: 500}); !errors.Is(err, ErrSVGTooLarge) {
		t.Errorf("want ErrSVGTooLarge, got %v", err)
	}
}

func TestAPageThatRunsOutOfTimeIsWrittenAsFarAsItGot(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 20000; i++ {
		content.WriteString("0 0 1 1 re f ")
	}
	d := onePage(t, [4]float64{0, 0, 30, 20}, content.String(), nil)
	var buf bytes.Buffer
	err := PageSVG(d, 1, &buf, SVGOptions{MaxDuration: time.Nanosecond})
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("want ErrTimedOut, got %v", err)
	}
	checkSVGDocument(t, buf.Bytes())
}

func TestNumbersAreNeverNaNOrInfinite(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{
		{math.NaN(), "0"}, {math.Inf(1), "10000000"}, {math.Inf(-1), "-10000000"},
		{-0.0001, "0"}, {1.5, "1.5"}, {2, "2"}, {-3.14159, "-3.142"}, {0.1 + 0.2, "0.3"},
	} {
		if got := num(c.in); got != c.want {
			t.Errorf("num(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   float64
		want string
	}{{math.NaN(), "0"}, {math.Inf(1), "1e+09"}, {0.0048828125, "0.004882812"}, {1, "1"}} {
		if got := matrixNum(c.in); got != c.want {
			t.Errorf("matrixNum(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAPathThatDoesNotStartWithAMoveStartsAtItsFirstMove(t *testing.T) {
	p := &userPath{}
	p.lineTo(1, 1)
	p.closePath()
	p.moveTo(2, 2)
	p.quadTo(3, 3, 4, 4)
	p.cubicTo(geometry.Point{X: 5, Y: 5}, geometry.Point{X: 6, Y: 6}, geometry.Point{X: 7, Y: 7})
	p.closePath()
	got := string(pathData(p, geometry.Identity()))
	if want := "M 2 2Q 3 3 4 4C 5 5 6 6 7 7Z"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if len(pathData(&userPath{}, geometry.Identity())) != 0 {
		t.Error("an empty path has data")
	}
}

// The device methods the interpreter reaches rarely are driven directly.
func TestTheDeviceMethodsDirectly(t *testing.T) {
	r := &renderer{img: raster.New(40, 40), fonts: map[int]*pdfFont{}, softMasks: map[softMaskKey][]uint8{}}
	dev := newSVGDevice(r, 1, 1<<20)
	r.dev = dev
	g := gstate{ctm: geometry.Identity(), fill: color.RGBA{R: 10, A: 255}, stroke: color.RGBA{G: 10, A: 255},
		fillSpace: deviceGray, strokeSpace: deviceGray, lineWidth: 1, miterLimit: 10, fillAlpha: 1, strokeAlpha: 1}
	p := &userPath{}
	p.moveTo(5, 5)
	p.lineTo(30, 5)
	p.lineTo(30, 30)
	p.closePath()

	dev.embolden(&g, p, g.ctm, 3, nil)
	g.fillAlpha = 0
	dev.embolden(&g, p, g.ctm, 3, nil)
	dev.fillPath(&g, p, g.ctm, vector.NonZero, nil)
	g.fillAlpha, g.strokeAlpha = 1, 0
	dev.strokePath(&g, p, g.ctm, nil)
	g.strokeAlpha = 1
	dev.fillPath(&g, &userPath{}, g.ctm, vector.NonZero, nil)
	dev.strokePath(&g, &userPath{}, g.ctm, nil)
	dev.embolden(&g, &userPath{}, g.ctm, 3, nil)

	// An image with no extent, and a masked one, which the rasteriser draws.
	dev.image(&g, &sampled{w: 0, h: 0})
	g.softMask = make([]uint8, 40*40)
	for i := range g.softMask {
		g.softMask[i] = 200
	}
	dev.image(&g, &sampled{w: 1, h: 1, pix: []uint8{9, 9, 9, 255}})
	g.ctm = geometry.Matrix{Xx: 20, Yy: 20}
	dev.image(&g, &sampled{w: 1, h: 1, pix: []uint8{9, 9, 9, 255}})
	dev.embolden(&g, p, geometry.Identity(), 3, nil)
	g.softMask = nil

	// A group that can reach nothing is not opened.
	g.softMask = make([]uint8, 40*40)
	box := [4]float64{-50, -50, -40, -40}
	if tok, ok := dev.beginGroup(&g, geometry.Identity(), &box); ok || tok != nil {
		t.Error("a group off the page was opened")
	}
	g.softMask = nil

	// Past its limit the device refuses everything.
	small := newSVGDevice(r, 1, 1)
	small.body = []byte("xx")
	if !small.full() || !small.full() {
		t.Error("a device past its limit is not full")
	}
	small.shade(&g, nil)
	if _, ok := small.beginGroup(&g, geometry.Identity(), nil); ok {
		t.Error("a full device opened a group")
	}
	small.image(&g, &sampled{w: 1, h: 1, pix: []uint8{0, 0, 0, 255}})
	small.fillPath(&g, p, g.ctm, vector.NonZero, nil)
	small.strokePath(&g, p, g.ctm, nil)
	small.embolden(&g, p, g.ctm, 1, nil)
	small.clipPath(&g, p, g.ctm, vector.NonZero, nil)
	checkSVGDocument(t, dev.document())
}

func TestTheRegionOfAMarkIsBoundedByThePage(t *testing.T) {
	r := &renderer{img: raster.New(40, 30)}
	dev := newSVGDevice(r, 1, 1<<20)
	if got := dev.page(); got != image.Rect(0, 0, 40, 30) {
		t.Errorf("page = %v", got)
	}
	p := &userPath{}
	p.moveTo(-1e300, math.NaN())
	p.quadTo(1e300, 5, 10, 10)
	p.cubicTo(geometry.Point{X: 1, Y: 1}, geometry.Point{X: 2, Y: 2}, geometry.Point{X: 20, Y: 20})
	p.closePath()
	if got := dev.extent(p, geometry.Identity(), 2); !got.In(dev.page()) {
		t.Errorf("extent = %v is outside the page", got)
	}
	if got := dev.extent(&userPath{}, geometry.Identity(), 2); !got.Empty() {
		t.Errorf("an empty path reaches %v", got)
	}
}
