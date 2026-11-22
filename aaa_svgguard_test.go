package render

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
)

func guardFixture() (*renderer, *svgDevice, gstate, *userPath) {
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
	return r, dev, g, p
}

func written(d *svgDevice) int { return len(d.body) + len(d.defs) }

// noPanic turns a crash into a failure that says what it was. These tests sit
// first in the package so that a guard taken away is reported by what it
// guards, not by whichever later test the crash happened to land in.
func noPanic(t *testing.T) {
	t.Helper()
	if e := recover(); e != nil {
		t.Errorf("panicked: %v", e)
	}
}

// A page that is not there writes nothing and says so; one that is there is
// written whole.
func TestPageSVGWritesOnlyAPageThatExists(t *testing.T) {
	defer noPanic(t)
	d := onePage(t, [4]float64{0, 0, 30, 20}, "0 g 0 0 5 5 re f", nil)
	var none strings.Builder
	if err := PageSVG(d, 9, &none, SVGOptions{}); err == nil || none.Len() != 0 {
		t.Errorf("a page that is not there: err %v, %d bytes", err, none.Len())
	}
	var buf strings.Builder
	if err := PageSVG(d, 1, &buf, SVGOptions{}); err != nil || !strings.HasPrefix(buf.String(), `<svg `) ||
		!strings.HasSuffix(buf.String(), `</svg>`) {
		t.Errorf("a page that is there: err %v, %q", err, buf.String())
	}
}

// Each guard that keeps a mark out of the document is held on both sides: the
// same mark is written when it shows and not when it does not.
func TestAMarkThatShowsNothingWritesNothing(t *testing.T) {
	_, dev, g, p := guardFixture()
	empty := &userPath{}

	marks := []struct {
		name string
		draw func(g *gstate, p *userPath)
	}{
		{"fill", func(g *gstate, p *userPath) { dev.fillPath(g, p, g.ctm, vector.NonZero, nil) }},
		{"stroke", func(g *gstate, p *userPath) { dev.strokePath(g, p, g.ctm, nil) }},
		{"embolden", func(g *gstate, p *userPath) { dev.embolden(g, p, g.ctm, 3, nil) }},
	}
	for _, m := range marks {
		before := written(dev)
		m.draw(&g, p)
		if written(dev) == before {
			t.Errorf("%s: a mark that shows was not written", m.name)
		}
		before = written(dev)
		m.draw(&g, empty)
		if written(dev) != before {
			t.Errorf("%s: a path with no segment was written", m.name)
		}
		faded := g
		faded.fillAlpha, faded.strokeAlpha = 0, 0
		m.draw(&faded, p)
		if written(dev) != before {
			t.Errorf("%s: an invisible mark was written", m.name)
		}
	}
}

func TestAlphaIsClampedToTheUnitInterval(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{-1, 0}, {0, 0}, {math.NaN(), 0}, {0.25, 0.25}, {1, 1}, {7, 1}, {math.Inf(1), 1}, {math.Inf(-1), 0},
	} {
		if got := unit(c.in); got != c.want {
			t.Errorf("unit(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if opacity("opacity", 1) != "" || opacity("opacity", 3) != "" || opacity("opacity", 0.5) != ` opacity="0.5"` {
		t.Error("an opacity of one is written, or one below one is not")
	}
}

func TestRegionsAreWholePixelsInsideThePage(t *testing.T) {
	_, dev, _, _ := guardFixture()
	nan := math.NaN()
	for _, c := range []struct {
		name           string
		x0, y0, x1, y1 float64
		want           image.Rectangle
	}{
		{"inside", 1.5, 2.5, 3.2, 4.2, image.Rect(1, 2, 4, 5)},
		{"below the page", -5, -9, 10, 10, image.Rect(0, 0, 10, 10)},
		{"past the page", 10, 10, 400, 900, image.Rect(10, 10, 40, 40)},
		{"NaN", nan, nan, nan, nan, image.Rect(0, 0, 0, 0)},
		{"infinite", math.Inf(-1), math.Inf(-1), math.Inf(1), math.Inf(1), image.Rect(0, 0, 40, 40)},
		{"exactly the edge", 0, 0, 40, 40, image.Rect(0, 0, 40, 40)},
	} {
		if got := dev.region(c.x0, c.y0, c.x1, c.y1); got != c.want {
			t.Errorf("%s: region = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestADashIsWrittenOnlyWhenSVGAcceptsIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		dash  []float64
		phase float64
		want  string
	}{
		{"plain", []float64{3, 2}, 0, ` stroke-dasharray="3 2"`},
		{"with a phase", []float64{3, 2}, 1, ` stroke-dasharray="3 2" stroke-dashoffset="1"`},
		{"negative length", []float64{3, -2}, 0, ""},
		{"NaN length", []float64{3, math.NaN()}, 0, ""},
		{"no length at all", []float64{0, 0}, 0, ""},
		{"none", nil, 0, ""},
	} {
		_, dev, _, _ := guardFixture()
		dev.dash(vector.StrokeStyle{Dash: c.dash, DashPhase: c.phase})
		if got := string(dev.body); got != c.want {
			t.Errorf("%s: wrote %q, want %q", c.name, got, c.want)
		}
	}
}

// Past its limit the device writes nothing and allocates nothing, whichever
// method it is asked through.
func TestAFullDeviceWritesNothingMore(t *testing.T) {
	defer noPanic(t)
	r, _, g, p := guardFixture()
	small := newSVGDevice(r, 1, 1)
	small.body = []byte("xx")
	if !small.full() {
		t.Fatal("a device past its limit is not full")
	}
	small.fillPath(&g, p, g.ctm, vector.NonZero, nil)
	small.strokePath(&g, p, g.ctm, nil)
	small.embolden(&g, p, g.ctm, 1, nil)
	small.clipPath(&g, p, g.ctm, vector.NonZero, nil)
	small.image(&g, &sampled{w: 1, h: 1, pix: []uint8{0, 0, 0, 255}})
	small.shade(&g, &shading{})
	if tok, ok := small.beginGroup(&g, geometry.Identity(), nil); ok || tok != nil {
		t.Error("a full device opened a group")
	}
	if string(small.body) != "xx" || len(small.defs) != 0 || small.nextID != 0 || len(small.images) != 0 {
		t.Errorf("a full device wrote: body %q, defs %q, %d ids", small.body, small.defs, small.nextID)
	}
	if small.white != nil {
		t.Error("a full device drew a mark with the rasteriser")
	}
}

func TestTheClipInForceWrapsWhatFollows(t *testing.T) {
	_, dev, g, p := guardFixture()
	if dev.open(&g) {
		t.Error("a group was opened with no clip")
	}
	dev.clipPath(&g, p, g.ctm, vector.EvenOdd, nil)
	if g.svgClip == "" || !strings.Contains(string(dev.defs), ` clip-rule="evenodd"`) {
		t.Errorf("clip %q, defs %q", g.svgClip, dev.defs)
	}
	if !dev.open(&g) || !strings.HasSuffix(string(dev.body), `<g clip-path="url(#`+g.svgClip+`)">`) {
		t.Errorf("a clip did not open a group: %q", dev.body)
	}
	dev.shut(true)
	dev.shut(false)
	if !strings.HasSuffix(string(dev.body), `</g>`) || strings.Count(string(dev.body), `</g>`) != 1 {
		t.Errorf("shut: %q", dev.body)
	}
}

func TestGroupsFadedAndMasked(t *testing.T) {
	defer noPanic(t)
	r, dev, g, _ := guardFixture()

	// Faded only: a <g>, closed by the matching </g>, and the renderer untouched.
	g.fillAlpha = 0.5
	tok, ok := dev.beginGroup(&g, geometry.Identity(), nil)
	grp, isGroup := tok.(*svgGroup)
	if !ok || !isGroup || grp.masked || string(dev.body) != `<g opacity="0.5">` {
		t.Fatalf("faded group: ok %v, token %#v, body %q", ok, tok, dev.body)
	}
	if _, raster := r.dev.(rasterDevice); raster {
		t.Error("a faded group moved drawing to the rasteriser")
	}
	dev.endGroup(&g, tok)
	if string(dev.body) != `<g opacity="0.5"></g>` {
		t.Errorf("faded group closed as %q", dev.body)
	}

	// Under a mask: drawn by the rasteriser on a paper of its own, and put back.
	g.fillAlpha = 1
	g.softMask = make([]uint8, 40*40)
	for i := range g.softMask {
		g.softMask[i] = 255
	}
	was := r.img
	tok, ok = dev.beginGroup(&g, geometry.Identity(), nil)
	grp, isGroup = tok.(*svgGroup)
	if !ok || !isGroup || !grp.masked || r.img == was {
		t.Fatalf("masked group: ok %v, token %#v", ok, tok)
	}
	if _, raster := r.dev.(rasterDevice); !raster {
		t.Error("a masked group was not drawn by the rasteriser")
	}
	before := len(dev.body)
	dev.endGroup(&g, tok)
	if r.img != was || r.dev != device(dev) {
		t.Error("the page and its device were not put back")
	}
	if len(dev.body) != before {
		t.Errorf("a masked group with nothing painted wrote %q", dev.body[before:])
	}
}

func TestAMaskedImageIsEmbeddedAsPixels(t *testing.T) {
	_, dev, g, _ := guardFixture()
	g.softMask = make([]uint8, 40*40)
	for i := range g.softMask {
		g.softMask[i] = 200
	}
	g.ctm = geometry.Matrix{Xx: 20, Yy: 20}
	dev.image(&g, &sampled{w: 1, h: 1, pix: []uint8{9, 9, 9, 255}})
	body := string(dev.body)
	if !strings.HasPrefix(body, `<image x="`) || strings.Contains(body, `<use`) {
		t.Errorf("a masked image was written as %q", body)
	}
	g.softMask = nil
	dev.image(&g, &sampled{w: 1, h: 1, pix: []uint8{9, 9, 9, 255}})
	if !strings.Contains(string(dev.body), `<use href="#i`) {
		t.Errorf("an unmasked image was not a use: %q", dev.body)
	}
}

func TestAMaskedMarkIsEmbeddedAsPixels(t *testing.T) {
	for _, c := range []struct {
		name string
		draw func(dev *svgDevice, g *gstate, p *userPath)
	}{
		{"fill", func(dev *svgDevice, g *gstate, p *userPath) { dev.fillPath(g, p, g.ctm, vector.NonZero, nil) }},
		{"stroke", func(dev *svgDevice, g *gstate, p *userPath) { dev.strokePath(g, p, g.ctm, nil) }},
		{"embolden", func(dev *svgDevice, g *gstate, p *userPath) { dev.embolden(g, p, g.ctm, 3, nil) }},
	} {
		_, dev, g, p := guardFixture()
		g.softMask = make([]uint8, 40*40)
		for i := range g.softMask {
			g.softMask[i] = 255
		}
		c.draw(dev, &g, p)
		if body := string(dev.body); !strings.HasPrefix(body, `<image x="`) || strings.Contains(body, `<path`) {
			t.Errorf("%s under a mask was written as %q", c.name, body)
		}
	}
}

func TestAMarkWithNowhereToLandDrawsNothing(t *testing.T) {
	_, dev, g, _ := guardFixture()
	called := false
	dev.fallback(&g, image.Rectangle{}, func(rasterDevice, *gstate) { called = true })
	dev.fallback(&g, image.Rect(100, 100, 200, 200), func(rasterDevice, *gstate) { called = true })
	if called || dev.white != nil || written(dev) != 0 {
		t.Errorf("a mark with an empty region reached the rasteriser: called %v", called)
	}
}
