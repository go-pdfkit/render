package render

import (
	"image/color"
	"math"
	"testing"

	"github.com/go-gfx/gfx/raster"
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
