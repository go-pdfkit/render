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
		r.device().clipPath(g, square, g.ctm, vector.NonZero, rect, true)
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
	if r.device().beginGroup(g, ctm, [4]float64{100, 100, 110, 110}, true) || len(r.groups) != 0 {
		t.Errorf("a box off the page opened a group: %d open", len(r.groups))
	}
	if !r.device().beginGroup(g, ctm, [4]float64{0, 0, 5, 5}, true) || len(r.groups) != 1 {
		t.Errorf("a box on the page did not open a group: %d open", len(r.groups))
	}
	r.device().endGroup(g)
	if len(r.groups) != 0 {
		t.Errorf("closing the group left %d open", len(r.groups))
	}
}

// A recorder is a device that says what it was asked to do and draws nothing,
// which is what the seam is for: the interpreter can be watched without a page.
type recorder struct {
	rasterDevice
	open               bool // what beginGroup answers
	began, ended, fill int
}

func (d *recorder) beginGroup(*gstate, geometry.Matrix, [4]float64, bool) bool {
	d.began++
	return d.open
}
func (d *recorder) endGroup(*gstate) { d.ended++ }
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

// A path is mapped by the matrix in force where it is PAINTED, not where it was
// written. A `cm` between the last segment and the painting operator is
// non-conforming (ISO 32000-1, Figure 9: the operators of a path object may not be
// interleaved with others), so a file has no business doing it, but a reader has to
// do something, and both references do the same thing: poppler maps at paint time
// (Gfx.cc doFill/doStroke over GfxState::getCTM, Splash.cc fillWithPattern's
// matrix) and MuPDF maps at paint time (pdf-op-run.c pdf_show_path with gstate.ctm).
// This renderer used to map each point as it arrived; it follows the references
// now, and this test pins it.
func TestAPathIsMappedWhereItIsPainted(t *testing.T) {
	for _, c := range []struct {
		name, content string
	}{
		{"fill", "10 10 20 20 re 2 0 0 2 0 0 cm f"},
		{"path built with lines", "10 10 m 30 10 l 30 30 l 10 30 l h 2 0 0 2 0 0 cm f"},
	} {
		d := onePage(t, [4]float64{0, 0, 100, 100}, c.content, nil)
		img := draw(t, d, Options{})
		// 20..60 in both axes once doubled; the page is 100 high, so the square
		// spans y = 40..80 down the image. Mapped as it was written it would span
		// 10..30 across and 70..90 down.
		wantBlack(t, img, 40, 60)
		wantWhite(t, img, 15, 80)
		wantWhite(t, img, 70, 60)
	}
}

// A glyph drawn while another is being drawn leaves the outer one alone. That
// happens when the glyph is filled with a pattern whose cell shows text: the
// outline buffer is lent to one glyph at a time, and the outer glyph is still
// stroked after its fill.
func TestAGlyphShownInsideAGlyphLeavesTheOuterOutline(t *testing.T) {
	_, f := fontOf(t, reader.Dict{"BaseFont": reader.Name("Helvetica")})
	outer, inner := 'O', 'l'
	want := map[rune]int{}
	for _, code := range []rune{outer, inner} {
		segs, ok := f.glyph(int(code))
		if !ok || len(segs) == 0 {
			t.Fatalf("no outline for %q", code)
		}
		want[code] = len(segs)
	}
	if want[outer] == want[inner] {
		t.Fatal("the two glyphs need outlines of different lengths for this test to see a mix-up")
	}

	r := &renderer{fonts: map[int]*pdfFont{}}
	d := &reentrant{r: r, f: f, inner: int(inner)}
	r.dev = d
	g := &gstate{ctm: geometry.Matrix{Xx: 1, Yy: 1}, fillAlpha: 1, strokeAlpha: 1}
	g.text.size, g.text.scale, g.text.mode = 10, 1, modeFillStroke
	// A glyph drawn beforehand leaves the buffer on the renderer for the next
	// one to reuse, which is the case that matters: with no buffer yet, the inner
	// glyph would make its own and nothing would be shared.
	d.fills = 1
	r.drawGlyph(g, f, int(inner), nil)
	if r.glyphPath == nil {
		t.Fatal("the buffer was not kept for the next glyph")
	}
	d.fills, d.stroked = 0, nil
	r.drawGlyph(g, f, int(outer), nil)

	// The inner glyph is stroked first, since it is drawn inside the outer fill.
	got := d.stroked
	if len(got) != 2 || got[0] != want[inner] || got[1] != want[outer] {
		t.Errorf("stroked outlines had %v segments, want [%d %d]", got, want[inner], want[outer])
	}
}

// A reentrant device shows another glyph in the middle of the first fill, as a
// pattern cell with text of its own does, and records what each stroke was given.
type reentrant struct {
	rasterDevice
	r       *renderer
	f       *pdfFont
	inner   int
	fills   int
	stroked []int
}

func (d *reentrant) fillPath(g *gstate, _ *userPath, _ geometry.Matrix, _ vector.FillRule, res reader.Dict) {
	if d.fills++; d.fills == 1 {
		d.r.drawGlyph(g, d.f, d.inner, res)
	}
}

func (d *reentrant) strokePath(_ *gstate, p *userPath, _ geometry.Matrix, _ reader.Dict) {
	d.stroked = append(d.stroked, len(p.segs))
}
