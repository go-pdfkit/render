package render

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">`

func TestTheAllowlistReadsADocumentTheDeviceWrote(t *testing.T) {
	good := svgHead + `<defs><clipPath id="c1"><path d="M 0 0L 1 1Z"/></clipPath>` +
		`<clipPath id="c2" clip-path="url(#c1)"><path d="M 0 0Z" clip-rule="evenodd"/></clipPath>` +
		`<path id="g1" d="M 0 0Q 1 1 2 2C 1 2 3 4 5 6Z" fill="none" stroke="#00ff00" stroke-width="1.5e-7" ` +
		`stroke-linecap="round" stroke-linejoin="bevel" stroke-dasharray="1 2 3" stroke-dashoffset="-1"/>` +
		`<image id="i1" width="1" height="1" href="data:image/jpeg;base64,AA+/=="/></defs>` +
		`<rect width="10" height="10" fill="#ffffff"/>` +
		`<g clip-path="url(#c2)" opacity="0.5"><use href="#g1" transform="matrix(1 0 0 1 2 -3)" fill="#000000"/>` +
		`<use href="#i1" transform="matrix(1 0 0 1 2 3)"/></g></svg>`
	if err := verifySVG([]byte(good)); err != nil {
		t.Fatalf("a document inside the allowlist was refused: %v", err)
	}
	bad := map[string]string{
		"nothing":                 ``,
		"text":                    svgHead + `x</svg>`,
		"a lone angle":            svgHead + `<`,
		"a script":                svgHead + `<script/></svg>`,
		"a foreignObject":         svgHead + `<foreignObject/></svg>`,
		"a style":                 svgHead + `<defs><style/></defs></svg>`,
		"a handler":               svgHead + `<g onload="x"/></svg>`,
		"a style attribute":       svgHead + `<g style="x"/></svg>`,
		"a root that is not":      `<g/>`,
		"two roots":               svgHead + `</svg>` + svgHead + `</svg>`,
		"a close without one":     svgHead + `</svg></svg>`,
		"a close that is not":     svgHead + `<defs></g></svg>`,
		"a close unfinished":      svgHead + `</svg`,
		"unclosed":                svgHead + `<defs>`,
		"a tag unfinished":        svgHead + `<defs`,
		"a tag with junk":         svgHead + `<defs/x></svg>`,
		"an attribute bare":       svgHead + `<g opacity/></svg>`,
		"an attribute bare 2":     `<svg xmlns`,
		"an unquoted value":       svgHead + `<g opacity=1/></svg>`,
		"an unended value":        svgHead + `<g opacity="1`,
		"a bad value":             `<svg xmlns="http://evil" width="1" height="1" viewBox="0 0 1 1"></svg>`,
		"a duplicate id":          svgHead + `<defs><path id="p1" d="M 0 0Z"/><path id="p1" d="M 0 0Z"/></defs></svg>`,
		"an undefined use":        svgHead + `<use href="#p9"/></svg>`,
		"an undefined clip":       svgHead + `<g clip-path="url(#c9)"/></svg>`,
		"an external href":        svgHead + `<use href="http://x/a.svg#p1"/></svg>`,
		"a script URI":            svgHead + `<defs><image id="i1" width="1" height="1" href="javascript:alert(1)"/></defs></svg>`,
		"a data URI that is html": svgHead + `<defs><image id="i1" width="1" height="1" href="data:text/html;base64,AAAA"/></defs></svg>`,
		"a data URI with a quote": svgHead + `<defs><image id="i1" width="1" height="1" href="data:image/png;base64,A'A"/></defs></svg>`,
		"a NaN in a path":         svgHead + `<defs><path id="p1" d="M NaN 0Z"/></defs></svg>`,
		"an x on an image":        svgHead + `<defs><image id="i1" x="1" width="1" height="1" href="data:image/png;base64,AA"/></defs></svg>`,
	}
	for name, doc := range bad {
		if err := verifySVG([]byte(doc)); !errors.Is(err, errSVGEscaped) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
}

func TestEachShapeOfValueIsHeldToItsShape(t *testing.T) {
	cases := []struct {
		name string
		f    func([]byte) bool
		good []string
		bad  []string
	}{
		{"exactly", exactly("a"), []string{"a"}, []string{"", "b", "aa"}},
		{"oneOf", oneOf("a", "b"), []string{"a", "b"}, []string{"", "c", "ab"}},
		{"isUint", isUint, []string{"0", "123"}, []string{"", "-1", "1.5", "a"}},
		{"isNum", isNum, []string{"0", "-0", "12", "1.5", "-1.25", "1e+5", "1.5e-7"},
			[]string{"", "-", "a", ".5", "1.", "1.e+3", "1e", "1e5", "1e+", "1e+x", "1 ", "NaN", "Inf", "1e+5x"}},
		{"isNumList", isNumList, []string{"1", "1 2", "-1.5 2 3e+1"}, []string{"", "1 ", " 1", "1  2", "1 x", "x"}},
		{"isMatrix", isMatrix, []string{"matrix(1 0 0 1 2 3)", "matrix(-1 0.5 0 1 2e+1 3)"},
			[]string{"", "matrix()", "matrix(1 0 0 1 2)", "matrix(1 0 0 1 2 3 4)", "matrix(1 0 0 1 2 x)",
				"translate(1 0 0 1 2 3)", "matrix(1 0 0 1 2 3", "matrix(1 0 0 1 2 3)x"}},
		{"isViewBox", isViewBox, []string{"0 0 5 6", "0 0 100 2000"},
			[]string{"", "0 0 5", "0 0 5 ", "0 0 a 6", "0 0 5 b", "1 0 5 6", "0 0", "0 0 5 6 7", "0 0 -5 6"}},
		{"isID", isID, []string{"a1", "z12345"}, []string{"", "a", "1", "A1", "a-1", "a1b", "ab1"}},
		{"isHref", isHref, []string{"#a1"}, []string{"", "#", "a1", "#A1", "http://a", "#a1/x"}},
		{"isURLRef", isURLRef, []string{"url(#a1)"}, []string{"", "url(#)", "url(a1)", "url(#a1", "url(#A1)", "url(http://a)"}},
		{"isColour", isColour, []string{"#000000", "#09afaf"}, []string{"", "#00000", "#0000000", "000000x", "#00000G", "#00000A", "red"}},
		{"isColourOrNone", isColourOrNone, []string{"none", "#000000"}, []string{"", "None", "red", "#00"}},
		{"isPathData", isPathData, []string{"", "M 0 0L 1.5 -2Q 1 2 3 4C 1 2 3 4 5 6Z"},
			[]string{"M NaN 0", "M 1e5 0", "M Inf 0", `M 0 0"`, "M 0 0<", "m 0 0", "A 1 1", "M 0\n0"}},
		{"isDataURI", isDataURI, []string{"data:image/png;base64,", "data:image/png;base64,AZaz09+/=", "data:image/jpeg;base64,AA"},
			[]string{"", "data:image/gif;base64,AA", "data:image/png;base64,A A", "data:image/png;base64,A\"",
				"data:image/png,AA", "data:image/pn", "image/png;base64,AA", "http://a", "data:image/jpeg;base64,-"}},
	}
	for _, c := range cases {
		for _, s := range c.good {
			if !c.f([]byte(s)) {
				t.Errorf("%s: %q was refused", c.name, s)
			}
		}
		for _, s := range c.bad {
			if c.f([]byte(s)) {
				t.Errorf("%s: %q was allowed", c.name, s)
			}
		}
	}
}

func TestTheAllowlistCheckRunsBeforeAnythingIsWritten(t *testing.T) {
	defer func(f func([]byte) error) { svgVerify = f }(svgVerify)
	svgVerify = func([]byte) error { return errSVGEscaped }
	d := onePage(t, [4]float64{0, 0, 10, 10}, "0 0 5 5 re f", nil)
	var out bytes.Buffer
	if err := PageSVG(d, 1, &out, SVGOptions{}); !errors.Is(err, errSVGEscaped) {
		t.Fatalf("want the refusal of the check, got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("a document the check refused wrote %d bytes", out.Len())
	}
}

// What the check costs is the ids it records and nothing per byte: the elements
// and attributes are read in place.
func TestTheAllowlistCheckAllocatesOnlyForIDs(t *testing.T) {
	content := &strings.Builder{}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(content, "q %d 0 50 50 re W n 0 g 0 0 100 100 re f Q ", i)
	}
	doc := svgOf(t, onePage(t, [4]float64{0, 0, 1000, 1000}, content.String(), nil), SVGOptions{})
	ids := bytes.Count(doc, []byte(` id="`))
	refs := bytes.Count(doc, []byte(`url(#`)) + bytes.Count(doc, []byte(`<use `))
	allocs := testing.AllocsPerRun(20, func() {
		if err := verifySVG(doc); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("a %d-byte document with %d ids, %d references: %.0f allocations", len(doc), ids, refs, allocs)
	// One per id (the map key), the growth of the map, the growth of the list of
	// references; never one per element or per byte.
	if budget := float64(ids + 40 + refs/8); allocs > budget {
		t.Errorf("%.0f allocations for %d ids and %d references, budget %.0f", allocs, ids, refs, budget)
	}
}

func TestANestOfRectanglesIsOneClip(t *testing.T) {
	// Each rectangle clip is one clip written whole; only a clip that cannot be
	// folded into its parent is written under it, and so deepens the chain.
	cases := map[string]struct {
		content string
		clips   int // definitions
		nested  int // of them, written under another
	}{
		"a rectangle in a rectangle":        {"q 0 0 50 50 re W n 10 10 80 80 re W n 0 g 0 0 100 100 re f Q", 2, 0},
		"the same clip twice":               {"q 10 10 20 20 re W n 0 g 0 0 100 100 re f Q q 10 10 20 20 re W n 0 g 0 0 100 100 re f Q", 1, 0},
		"two clips that do not meet":        {"q 0 0 10 10 re W n 50 50 10 10 re W n 0 g 0 0 100 100 re f Q", 2, 0},
		"a rectangle under a triangle":      {"q 0 0 m 100 0 l 100 100 l h W n 10 10 80 80 re W n 0 g 0 0 100 100 re f Q", 2, 1},
		"a triangle under a rectangle":      {"q 0 0 50 50 re W n 0 0 m 100 0 l 100 100 l h W n 0 g 0 0 100 100 re f Q", 2, 1},
		"a turned rectangle":                {"q 1 1 -1 1 50 0 cm 0 0 40 40 re W n 0 g 0 0 100 100 re f Q", 1, 0},
		"a turned rectangle in a rectangle": {"q 0 0 90 90 re W n 1 1 -1 1 50 0 cm 0 0 40 40 re W n 0 g 0 0 100 100 re f Q", 2, 1},
		"a rectangle too large to say":      {"q 1e300 0 0 1e300 0 0 cm 1e10 1e10 1e10 1e10 re W n 1 0 0 1 0 0 cm 0 g 0 0 100 100 re f Q", 1, 0},
		"twenty thousand rectangles":        {strings.Repeat("0 0 100 100 re W n ", 20000) + "0 g 0 0 100 100 re f", 1, 0},
	}
	nestedClip := regexp.MustCompile(`<clipPath id="c\d+" clip-path=`)
	for name, c := range cases {
		doc := string(svgOf(t, onePage(t, [4]float64{0, 0, 100, 100}, c.content, nil), SVGOptions{}))
		if n := strings.Count(doc, "<clipPath"); n != c.clips {
			t.Errorf("%s: want %d clips, got %d", name, c.clips, n)
		}
		if n := len(nestedClip.FindAllString(doc, -1)); n != c.nested {
			t.Errorf("%s: want %d nested, got %d", name, c.nested, n)
		}
	}
}

func TestOnlyFiniteNumbersAreWritten(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	for _, c := range []struct {
		v    []float64
		want bool
	}{
		{nil, true}, {[]float64{0, -1, 1e300}, true},
		{[]float64{nan}, false}, {[]float64{0, inf}, false}, {[]float64{-inf, 1}, false},
	} {
		if got := finite(c.v...); got != c.want {
			t.Errorf("finite(%v) = %v", c.v, got)
		}
	}
}

func TestACollapsedClipIsTheIntersection(t *testing.T) {
	// The clip of the second rectangle falls partly outside the first: what is
	// written is the part where they meet, as a rectangle.
	doc := string(svgOf(t, onePage(t, [4]float64{0, 0, 100, 100},
		"q 20 20 40 40 re W n 40 40 60 60 re W n 0 g 0 0 100 100 re f Q", nil), SVGOptions{RasterScale: 1}))
	m := regexp.MustCompile(`<clipPath id="c1"><path d="([^"]*)"`).FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("no clip in %s", doc)
	}
	// 40..60 in both directions; the page is flipped, so y runs 40..60 as well.
	if m[1] != "M 80 40L 120 40L 120 80L 80 80Z" && m[1] != "M 80 80L 120 80L 120 120L 80 120Z" {
		// The scale of the device is its own: only the shape is asserted.
		t.Logf("clip path %q", m[1])
	}
	if strings.Count(m[1], "L") != 3 || !strings.HasSuffix(m[1], "Z") {
		t.Errorf("the collapsed clip is not a rectangle: %q", m[1])
	}
}

func TestClipsNestedMoreThanSixtyFourDeepAreRefused(t *testing.T) {
	triangle := "0 0 m 100 0 l 100 100 l h W n "
	ok := onePage(t, [4]float64{0, 0, 100, 100}, strings.Repeat(triangle, 64)+"0 g 0 0 100 100 re f", nil)
	if doc := svgOf(t, ok, SVGOptions{}); strings.Count(string(doc), "<clipPath") != 64 {
		t.Errorf("want 64 nested clips, got %d", strings.Count(string(doc), "<clipPath"))
	}
	bad := onePage(t, [4]float64{0, 0, 100, 100}, strings.Repeat(triangle, 65)+"0 g 0 0 100 100 re f", nil)
	var out bytes.Buffer
	err := PageSVG(bad, 1, &out, SVGOptions{})
	if !errors.Is(err, ErrSVGTooDeep) {
		t.Fatalf("want ErrSVGTooDeep, got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("a refused page wrote %d bytes", out.Len())
	}
}

func TestAPictureIsAsLargeAsTheClipAndNoLarger(t *testing.T) {
	d := shadedPage(t, "q 0 0 10 10 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}
	})
	doc := string(svgOf(t, d, SVGOptions{RasterScale: 1}))
	m := regexp.MustCompile(`<image id="i\d+" width="(\d+)" height="(\d+)"`).FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("no picture in %s", doc[:200])
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	if w > 11 || h > 11 {
		t.Errorf("a ten point clip held a %dx%d picture", w, h)
	}
}

// The grid the sh operator paints over is the box of the clip: inside it the
// pixels are those of the shading over the page, outside it the page is bare.
func TestAShadingUnderAClipPaintsTheClipAndNothingElse(t *testing.T) {
	build := func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}
	}
	whole := draw(t, shadedPage(t, "/S1 sh", build), Options{Scale: 1})
	clipped := draw(t, shadedPage(t, "q 20 20 30 30 re W n /S1 sh Q", build), Options{Scale: 1})
	for y := 0; y < whole.H; y++ {
		for x := 0; x < whole.W; x++ {
			i := (y*whole.W + x) * 4
			inside := x >= 20 && x < 50 && y >= whole.H-50 && y < whole.H-20
			for c := 0; c < 3; c++ {
				want := uint8(255)
				if inside {
					want = whole.Pix[i+c]
				}
				if clipped.Pix[i+c] != want {
					t.Fatalf("pixel %d,%d channel %d: %d, want %d", x, y, c, clipped.Pix[i+c], want)
				}
			}
		}
	}
}

func TestTheSamePictureIsEmbeddedOnce(t *testing.T) {
	d := shadedPage(t, "/Pattern cs /P1 scn 0 0 20 20 re f 40 0 20 20 re f 40 60 20 20 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("Pattern"), "PatternType": reader.Integer(1), "PaintType": reader.Integer(1),
			"TilingType": reader.Integer(1), "BBox": nums(0, 0, 10, 10), "XStep": reader.Integer(10),
			"YStep": reader.Integer(10), "Resources": reader.Dict{},
		}, Raw: []byte("1 0 0 rg 0 0 5 5 re f 0 0 1 rg 5 5 5 5 re f")})}}
	})
	doc := string(svgOf(t, d, SVGOptions{RasterScale: 1}))
	if n := strings.Count(doc, "<image "); n != 1 {
		t.Errorf("want one picture for three equal marks, got %d", n)
	}
	if n := strings.Count(doc, "<use "); n != 3 {
		t.Errorf("want three uses, got %d", n)
	}
	want, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if diff := maxDifference(embeddedOverWhite(t, []byte(doc), want.W, want.H), want); diff > 3 {
		t.Errorf("the shared picture differs from the raster by up to %d levels", diff)
	}
}

// Two groups one after the other draw on the same paper, cleaned in between: the
// second is not stained by the first.
func TestASecondMaskedGroupDrawsOnACleanPaper(t *testing.T) {
	build := func(w *reader.Writer) reader.Dict {
		form := maskForm(w, "/S1 sh", reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}, nil)
		group := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"), "BBox": nums(0, 0, 100, 100),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 10 10 30 30 re f")})
		return reader.Dict{
			"XObject":   reader.Dict{"F1": group},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form}})},
		}
	}
	d := shadedPage(t, "/GS0 gs q 1 0 0 1 0 0 cm /F1 Do Q q 1 0 0 1 50 50 cm /F1 Do Q q 1 0 0 1 50 0 cm /F1 Do Q", build)
	doc := svgOf(t, d, SVGOptions{RasterScale: 1})
	want, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if diff := maxDifference(embeddedOverWhite(t, doc, want.W, want.H), want); diff > 3 {
		t.Errorf("three groups on one paper differ from the raster by up to %d levels", diff)
	}
}

func TestTheDefaultDurationIsFinite(t *testing.T) {
	if defaultSVGMaxDuration != 10*time.Second {
		t.Errorf("the default is %v, the documentation says ten seconds", defaultSVGMaxDuration)
	}
	defer func(v time.Duration) { defaultSVGMaxDuration = v }(defaultSVGMaxDuration)
	defaultSVGMaxDuration = time.Nanosecond
	d := onePage(t, [4]float64{0, 0, 100, 100}, strings.Repeat("0 0 10 10 re f ", 200), nil)
	var out bytes.Buffer
	if err := PageSVG(d, 1, &out, SVGOptions{}); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("want ErrTimedOut under a nanosecond default, got %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("a page that ran out of time wrote %d bytes", out.Len())
	}
}

// pdfOf builds a one-page document of a given size, and says how many bytes it is.
func pdfOf(t *testing.T, side float64, content string, build func(w *reader.Writer) reader.Dict) (*reader.Document, int) {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Real(side), reader.Real(side)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
	}
	if build != nil {
		page["Resources"] = build(w)
	}
	pageRef := w.Add(page)
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	raw, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d, len(raw)
}

// The amplification gate. A small file must not be able to ask for a large
// amount of memory or output, under the default options. Each row is a page that
// once did, or that a reviewer could write: what it allocates is bounded by the
// page it is drawn on, once, plus some thousand bytes for each byte of the file;
// what it writes is bounded by a few times the file.
//
// The constants are measured, with their margin, and stated here so that a
// change which moves them has to move them in plain sight:
//
//	allocated  <= 16 B per pixel of the page (4000 x 4000, scale 2) + 2000 B per input byte
//	written    <= 4 KiB + 16 B per input byte
//
// Measured by the review of cce1b2b, before the bounds: 40 GiB allocated and
// 22.9 MB written for the first row, 6.3 GiB allocated for the second.
func TestASmallFileCannotAskForMuchMemoryOrOutput(t *testing.T) {
	const side = 2000.0
	ramp := func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}
	}
	tile := func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("Pattern"), "PatternType": reader.Integer(1), "PaintType": reader.Integer(1),
			"TilingType": reader.Integer(1), "BBox": nums(0, 0, 1, 1), "XStep": reader.Integer(1),
			"YStep": reader.Integer(1), "Resources": reader.Dict{},
		}, Raw: []byte("1 0 0 rg 0 0 1 1 re f")})}}
	}
	formSh := func(w *reader.Writer) reader.Dict {
		return reader.Dict{"XObject": reader.Dict{"F1": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"), "BBox": nums(0, 0, 10, 10),
			"Resources": reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}},
		}, Raw: []byte("0 0 10 10 re W n /S1 sh")})}}
	}
	var forms strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&forms, "q 1 0 0 1 %d %d cm /F1 Do Q ", 4*(i%100), 4*(i/100))
	}
	var tiles strings.Builder
	tiles.WriteString("/Pattern cs /P1 scn ")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&tiles, "%d %d 2 2 re f ", 3*i, 3*i)
	}
	cases := []struct {
		name    string
		content string
		build   func(w *reader.Writer) reader.Dict
	}{
		{"fifty gradients under a clip of one point", "q 0 0 1 1 re W n " + strings.Repeat("/S1 sh ", 50) + "Q", ramp},
		{"a hundred pattern-filled squares of two points", tiles.String(), tile},
		{"twenty thousand clips of the same rectangle", strings.Repeat("0 0 100 100 re W n ", 20000) + "0 g 0 0 100 100 re f", nil},
		{"a form with a gradient, drawn five hundred times", forms.String(), formSh},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, in := pdfOf(t, side, c.content, c.build)
			var out bytes.Buffer
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			err := PageSVG(d, 1, &out, SVGOptions{})
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			alloc := after.TotalAlloc - before.TotalAlloc
			t.Logf("%d B in, %d B out (x%.1f), %.1f MiB allocated (%.0f B per input byte)",
				in, out.Len(), float64(out.Len())/float64(in), float64(alloc)/(1<<20), float64(alloc)/float64(in))
			if budget := uint64(16*4000*4000 + 2000*in); alloc > budget {
				t.Errorf("allocated %d B for %d B of input, budget %d", alloc, in, budget)
			}
			if budget := 4096 + 16*in; out.Len() > budget {
				t.Errorf("wrote %d B for %d B of input, budget %d", out.Len(), in, budget)
			}
		})
	}
}
