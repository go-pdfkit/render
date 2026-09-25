package render

import (
	"image/color"
	"math"
	"testing"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

func TestAClipThatIsNotThereLetsEverythingThrough(t *testing.T) {
	// Every place that asks the clip how much of a pixel it allows checks
	// first whether there is one; the method answers for itself as well, so
	// that adding a place cannot make a page vanish.
	var none *clip
	if got := none.at(0, 0); got != 1 {
		t.Errorf("no clip at all let through %v", got)
	}
}

func TestTwoClipsThatDoNotOverlapLetNothingThrough(t *testing.T) {
	// Clipping to one corner and then to another leaves nothing: what is left
	// is what both allow, and they allow nothing in common.
	d := shadedPage(t, "q 0 0 40 40 re W n 60 60 40 40 re W n 0 0 100 100 re f Q",
		func(w *reader.Writer) reader.Dict { return reader.Dict{} })
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range [][2]int{{20, 20}, {20, 80}, {80, 20}, {80, 80}, {50, 50}} {
		wantWhite(t, img, at[0], at[1])
	}
}

func TestAClipKeepsToItsOwnCorner(t *testing.T) {
	// And when they do overlap, what is drawn is the overlap and nothing
	// else — which is what says the box was narrowed rather than forgotten.
	d := shadedPage(t, "q 0 0 60 60 re W n 40 40 60 60 re W n 0 0 100 100 re f Q",
		func(w *reader.Writer) reader.Dict { return reader.Dict{} })
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The overlap is x 40..60, y 40..60 in the page's own coordinates, which
	// is the middle of the image with its y counted the other way.
	wantColour(t, img, 50, 50, color.RGBA{A: 255}, 4)
	for _, at := range [][2]int{{20, 80}, {80, 20}, {20, 20}, {80, 80}} {
		wantWhite(t, img, at[0], at[1])
	}
}

// TestALongChainIsComposedRatherThanWalked. A clip holds its own shape and a
// pointer to the one it narrowed, and the product is taken at the pixels that
// are painted -- which is right until a page says `W n` without end, when
// every painted pixel would walk the lot. Past clipDepth the chain is composed
// into one grid, and this is the only thing that pulls that brake: no page in
// either corpus nests clips that deep.
func TestALongChainIsComposedRatherThanWalked(t *testing.T) {
	r := &renderer{img: raster.New(8, 8)}
	g := &gstate{}

	half := make([]float64, 64)
	for i := range half {
		half[i] = 0.5
	}
	want := 1.0
	for i := 1; i <= clipDepth+3; i++ {
		r.narrow(g, half, 0, 0, 8, 8, true)
		want *= 0.5
		if g.clip.depth > clipDepth {
			t.Fatalf("after %d narrowings the chain is %d deep, past the bound of %d",
				i, g.clip.depth, clipDepth)
		}
		// Whatever it chose to store, it must answer the same thing.
		if got := g.clip.at(4, 4); math.Abs(got-want) > 1e-6 {
			t.Errorf("after %d narrowings at(4,4) = %v, want %v", i, got, want)
		}
	}
	// It did compose: a chain that was only ever extended would be deeper
	// than the bound by now.
	if g.clip.depth > clipDepth {
		t.Errorf("depth = %d, want it composed back under %d", g.clip.depth, clipDepth)
	}
	// And a composed clip carries no chain under it.
	for k := g.clip; k != nil; k = k.under {
		if k.under != nil && k.under.under != nil && k.depth <= 1 {
			t.Error("a composed clip still points at what it composed")
		}
	}
}

// TestAChainStopsAtTheFirstShapeThatHidesThePixel. Walking a chain multiplies,
// so a zero anywhere in it means zero and the rest need not be read.
func TestAChainStopsAtTheFirstShapeThatHidesThePixel(t *testing.T) {
	r := &renderer{img: raster.New(8, 8)}
	g := &gstate{}

	hole := make([]float64, 64)
	for i := range hole {
		hole[i] = 1
	}
	hole[4*8+4] = 0 // one pixel hidden by the first shape
	half := make([]float64, 64)
	for i := range half {
		half[i] = 0.5
	}
	r.narrow(g, hole, 0, 0, 8, 8, true)
	r.narrow(g, half, 0, 0, 8, 8, true)

	if got := g.clip.at(4, 4); got != 0 {
		t.Errorf("at(4,4) = %v, want 0: the first shape hides it", got)
	}
	if got := g.clip.at(3, 3); math.Abs(got-0.5) > 1e-6 {
		t.Errorf("at(3,3) = %v, want 0.5", got)
	}
}

// TestAClipThatCoversNoPixelAnswersForEveryPixel covers the empty clip, which
// carries no grid at all and must still say no everywhere.
func TestAClipThatCoversNoPixelAnswersForEveryPixel(t *testing.T) {
	r := &renderer{img: raster.New(8, 8)}
	g := &gstate{}
	r.narrow(g, nil, 0, 0, 0, 0, false)
	for _, p := range [][2]int{{0, 0}, {4, 4}, {7, 7}, {-1, -1}} {
		if got := g.clip.at(p[0], p[1]); got != 0 {
			t.Errorf("at(%d,%d) = %v, want 0", p[0], p[1], got)
		}
	}
	// And narrowing an empty clip further leaves it empty.
	whole := make([]float64, 64)
	for i := range whole {
		whole[i] = 1
	}
	r.narrow(g, whole, 0, 0, 8, 8, true)
	if got := g.clip.at(4, 4); got != 0 {
		t.Errorf("at(4,4) = %v after narrowing an empty clip, want 0", got)
	}
}

// TestARectangleClipAnswersWithoutAGrid. A clip whose shape was known to be a
// rectangle before it was built carries four numbers instead of w*h of them,
// and works out what it lets through. What it must answer is what the same
// rectangle rasterised would have: whole inside, partly on a fractional edge,
// nothing outside.
func TestARectangleClipAnswersWithoutAGrid(t *testing.T) {
	r := &renderer{img: raster.New(20, 20)}
	g := &gstate{}
	rect := vector.Rect{X0: 4.5, Y0: 4, X1: 12, Y1: 11}
	ox, oy, w, h, ok := rect.Box(20, 20)
	r.narrowRect(g, rect, ox, oy, w, h, ok)

	if g.clip == nil || g.clip.rect == nil {
		t.Fatal("the clip kept no rectangle")
	}
	if g.clip.cov != nil {
		t.Error("the clip allocated a grid for a shape four numbers describe")
	}
	// Inside, whole: answered from the box, with no arithmetic.
	if got := g.clip.at(8, 7); got != 1 {
		t.Errorf("at(8,7) inside = %v, want 1", got)
	}
	// The left edge falls mid-pixel, so that column is half-covered.
	if got := g.clip.at(4, 7); got <= 0 || got >= 1 {
		t.Errorf("at(4,7) on a fractional edge = %v, want something between 0 and 1", got)
	}
	// Outside the box entirely.
	for _, p := range [][2]int{{2, 7}, {15, 7}, {8, 1}, {8, 18}} {
		if got := g.clip.at(p[0], p[1]); got != 0 {
			t.Errorf("at(%d,%d) outside = %v, want 0", p[0], p[1], got)
		}
	}
	// And it agrees with the same rectangle rasterised, pixel for pixel.
	rz := &vector.Rasterizer{}
	path := vector.NewPath()
	path.MoveTo(rect.X0, rect.Y0)
	path.LineTo(rect.X1, rect.Y0)
	path.LineTo(rect.X1, rect.Y1)
	path.LineTo(rect.X0, rect.Y1)
	path.Close()
	cov, fox, foy, fw, fh, fok := rz.Fill(path, vector.NonZero, 20, 20)
	if !fok {
		t.Fatal("the rasteriser drew nothing")
	}
	var grid gstate
	r.narrow(&grid, cov, fox, foy, fw, fh, fok)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			if a, b := g.clip.at(x, y), grid.clip.at(x, y); a != b {
				t.Fatalf("at(%d,%d): rectangle %.17g, grid %.17g", x, y, a, b)
			}
		}
	}
}

// TestARectangleClipThatHidesAPixelStopsTheChain. A zero anywhere in a chain
// means zero, and the rest of it need not be asked.
func TestARectangleClipThatHidesAPixelStopsTheChain(t *testing.T) {
	r := &renderer{img: raster.New(20, 20)}
	g := &gstate{}
	// Two rectangles that overlap in a strip: outside the strip one of them
	// hides the pixel, and its partial edge is what the other multiplies.
	a := vector.Rect{X0: 2, Y0: 2, X1: 10.5, Y1: 18}
	b := vector.Rect{X0: 4, Y0: 4, X1: 16, Y1: 16}
	for _, rect := range []vector.Rect{a, b} {
		ox, oy, w, h, ok := rect.Box(20, 20)
		r.narrowRect(g, rect, ox, oy, w, h, ok)
	}
	if got := g.clip.at(6, 8); got != 1 {
		t.Errorf("at(6,8), inside both = %v, want 1", got)
	}
	if got := g.clip.at(10, 8); got <= 0 || got >= 1 {
		t.Errorf("at(10,8), on the first rectangle's fractional edge = %v, want a fraction", got)
	}
	if got := g.clip.at(12, 8); got != 0 {
		t.Errorf("at(12,8), past the first rectangle = %v, want 0", got)
	}
}

// TestARectangleThatCoversNoPixel. A `re` with no area clips everything away,
// and narrowRect must say so rather than keep a rectangle nothing is inside.
func TestARectangleThatCoversNoPixel(t *testing.T) {
	r := &renderer{img: raster.New(20, 20)}
	g := &gstate{}
	rect := vector.Rect{X0: 5, Y0: 5, X1: 5, Y1: 9}
	ox, oy, w, h, ok := rect.Box(20, 20)
	if ok {
		t.Fatal("an empty rectangle claims a box")
	}
	r.narrowRect(g, rect, ox, oy, w, h, ok)
	if g.clip == nil || g.clip.at(5, 5) != 0 {
		t.Error("an empty rectangle still lets something through")
	}
}

// TestARectangleThinnerThanASubScanline. The rasteriser samples four
// sub-scanlines a row, at y+0.125, 0.375, 0.625 and 0.875. A rectangle that
// falls BETWEEN two of them has a box -- the row is within floor(Y0)..ceil(Y1)
// -- and covers nothing in it.
//
// It is worth pinning because it is where a clip that let a pixel through
// would be the difference between a mark and no mark, and because it is the
// one case in which a rectangle link answers zero: the chain can stop there
// without asking the rest.
func TestARectangleThinnerThanASubScanline(t *testing.T) {
	r := &renderer{img: raster.New(20, 20)}
	rect := vector.Rect{X0: 4, Y0: 5.0, X1: 12, Y1: 5.1}
	ox, oy, w, h, ok := rect.Box(20, 20)
	if !ok {
		t.Fatal("a rectangle with area claims no box")
	}
	if oy != 5 || h != 1 {
		t.Fatalf("box rows %d..%d, want just row 5", oy, oy+h)
	}
	// It is in the box and covers nothing of it.
	if got := rect.At(8, 5); got != 0 {
		t.Errorf("At(8,5) = %v, want 0: the rectangle is between two sub-scanlines", got)
	}

	// And a chain stops at it: the second rectangle is never asked.
	g := &gstate{}
	r.narrowRect(g, rect, ox, oy, w, h, ok)
	wide := vector.Rect{X0: 0, Y0: 0, X1: 20, Y1: 20}
	wox, woy, ww, wh, wok := wide.Box(20, 20)
	r.narrowRect(g, wide, wox, woy, ww, wh, wok)
	if got := g.clip.at(8, 5); got != 0 {
		t.Errorf("at(8,5) through the chain = %v, want 0", got)
	}

	// What the rasteriser makes of the same rectangle, for comparison.
	rz := &vector.Rasterizer{}
	path := vector.NewPath()
	path.MoveTo(rect.X0, rect.Y0)
	path.LineTo(rect.X1, rect.Y0)
	path.LineTo(rect.X1, rect.Y1)
	path.LineTo(rect.X0, rect.Y1)
	path.Close()
	if cov, fox, foy, fw, fh, fok := rz.Fill(path, vector.NonZero, 20, 20); fok {
		if fox != ox || foy != oy || fw != w || fh != h {
			t.Errorf("boxes differ: rectangle (%d,%d,%d,%d), rasteriser (%d,%d,%d,%d)",
				ox, oy, w, h, fox, foy, fw, fh)
		}
		for i, v := range cov {
			if v != rect.At(fox+i%fw, foy+i/fw) {
				t.Fatalf("pixel %d: rasteriser %v, rectangle %v", i, v, rect.At(fox+i%fw, foy+i/fw))
			}
		}
	}
}
