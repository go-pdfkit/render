package render

import (
	"image/color"
	"testing"

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
