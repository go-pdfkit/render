package render

import (
	"testing"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

// A path that is painted and then clipped is mapped once: the second ask with
// the same matrix gets the answer kept from the first, and any change to the
// segments or to the matrix gets a fresh one. The kept answer is marked by hand
// here, since a cache that re-did the work would give the same shape.
func TestAUserPathIsMappedOncePerMatrix(t *testing.T) {
	u := &userPath{}
	u.moveTo(1, 2)
	u.lineTo(3, 4)
	u.quadTo(5, 6, 7, 8)
	u.cubicTo(geometry.Point{X: 1, Y: 1}, geometry.Point{X: 2, Y: 2}, geometry.Point{X: 3, Y: 3})
	u.closePath()
	m := geometry.Matrix{Xx: 2, Yy: 3, X0: 10, Y0: 20}

	first := u.device(m)
	if first.Len() != 5 {
		t.Fatalf("mapped path has %d segments, want 5", first.Len())
	}
	first.MoveTo(99, 99) // the mark
	if got := u.device(m).Len(); got != 6 {
		t.Errorf("same matrix, same segments: %d segments, want the kept 6", got)
	}

	other := geometry.Matrix{Xx: 1, Yy: 1}
	if got := u.device(other).Len(); got != 5 {
		t.Errorf("another matrix: %d segments, want 5 remapped", got)
	}
	u.device(m).MoveTo(99, 99)
	u.lineTo(9, 9)
	if got := u.device(m).Len(); got != 6 {
		t.Errorf("after adding a segment: %d segments, want 6 remapped", got)
	}
	u.device(m).MoveTo(99, 99)
	u.reset()
	u.moveTo(0, 0)
	if got := u.device(m).Len(); got != 1 {
		t.Errorf("after a reset: %d segments, want 1 remapped", got)
	}
}

// A rectangle clip is carried as four numbers while the transform keeps it
// axis-aligned, and as the grid of the shape it is once it does not. The two
// paint alike, so only the link they leave tells them apart.
func TestARectangleClipIsCarriedAsARectangleOnlyWhenAxisAligned(t *testing.T) {
	rect := [4]float64{5, 5, 10, 10}
	square := &userPath{}
	addRect(square, rect[:])

	for _, c := range []struct {
		name string
		ctm  geometry.Matrix
		want bool
	}{
		{"scaled", geometry.Matrix{Xx: 1, Yy: 1}, true},
		{"sheared", geometry.Matrix{Xx: 1, Yx: 1, Yy: 1}, false},
		{"skewed", geometry.Matrix{Xx: 1, Xy: 1, Yy: 1}, false},
	} {
		r := &renderer{img: raster.New(40, 40)}
		g := &gstate{ctm: c.ctm}
		r.device().clipPath(g, square, g.ctm, vector.NonZero, &rect)
		if g.clip == nil {
			t.Fatalf("%s: no clip was made", c.name)
		}
		if got := g.clip.rect != nil; got != c.want {
			t.Errorf("%s: carried as a rectangle = %v, want %v", c.name, got, c.want)
		}
		if got := g.clip.cov != nil; got == c.want {
			t.Errorf("%s: carried as a grid = %v, want %v", c.name, got, !c.want)
		}
	}
}

// A group whose box reaches nothing on the page has nothing to run and nothing
// to put back.
func TestAGroupBoxOffThePageReachesNothing(t *testing.T) {
	r := &renderer{img: raster.New(10, 10)}
	g := &gstate{}
	ctm := geometry.Matrix{Xx: 1, Yy: 1}
	if tok, ok := r.device().beginGroup(g, ctm, &[4]float64{100, 100, 110, 110}); ok || tok != nil {
		t.Errorf("a box off the page opened a group: token %v, ok %v", tok, ok)
	}
	if tok, ok := r.device().beginGroup(g, ctm, &[4]float64{0, 0, 5, 5}); !ok || tok == nil {
		t.Errorf("a box on the page did not open a group: token %v, ok %v", tok, ok)
	}
}

// A recorder is a device that says what it was asked to do and draws nothing,
// which is what the seam is for: the interpreter can be watched without a page.
type recorder struct {
	rasterDevice
	open               bool // what beginGroup answers
	began, ended, fill int
}

func (d *recorder) beginGroup(*gstate, geometry.Matrix, *[4]float64) (any, bool) {
	d.began++
	return nil, d.open
}
func (d *recorder) endGroup(*gstate, any) { d.ended++ }
func (d *recorder) fillPath(*gstate, *userPath, geometry.Matrix, vector.FillRule, reader.Dict) {
	d.fill++
}

// A group that can reach nothing runs none of its content and is not closed; one
// that can reach something runs it between the two calls.
func TestAGroupThatReachesNothingRunsNothing(t *testing.T) {
	for _, c := range []struct {
		open                bool
		fill, began, ending int
	}{
		{false, 0, 1, 0},
		{true, 1, 1, 1},
	} {
		d := &recorder{open: c.open}
		r := &renderer{img: raster.New(10, 10), dev: d}
		r.runGroup(&gstate{}, []byte("0 0 5 5 re f"), nil, gstate{}, [4]float64{}, false)
		if d.began != c.began || d.fill != c.fill || d.ended != c.ending {
			t.Errorf("open=%v: began %d, filled %d, ended %d; want %d, %d, %d",
				c.open, d.began, d.fill, d.ended, c.began, c.fill, c.ending)
		}
	}
}
