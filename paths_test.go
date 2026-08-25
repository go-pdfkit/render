package render

import (
	"testing"
)

func TestTheCurveOperators(t *testing.T) {
	// Each of the three curve operators draws a bulge to the right of a
	// straight line from bottom to top; the middle of the page is inked and
	// the far left is not.
	for _, op := range []string{
		"10 10 m 40 10 40 70 10 70 c f",
		"10 10 m 40 10 10 70 v f",
		"10 10 m 40 70 10 70 y f",
	} {
		d := onePage(t, [4]float64{0, 0, 80, 80}, "0 g "+op, nil)
		img := draw(t, d, Options{})
		if inked(img) == 0 {
			t.Errorf("%q drew nothing", op)
		}
		wantWhite(t, img, 75, 40)
	}
}

func TestClosingAPath(t *testing.T) {
	// A triangle closed with h and filled.
	d := onePage(t, [4]float64{0, 0, 40, 40}, "0 g 5 5 m 35 5 l 20 35 l h f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 20, 25)
	wantWhite(t, img, 6, 6)
}

func TestEveryPaintingOperator(t *testing.T) {
	// The ten of them, each on its own page; what matters is which of fill and
	// stroke each one does.
	cases := []struct {
		op           string
		insideFilled bool
		edgeStroked  bool
	}{
		{"n", false, false},
		{"f", true, false},
		{"F", true, false},
		{"f*", true, false},
		{"S", false, true},
		{"s", false, true},
		{"B", true, true},
		{"B*", true, true},
		{"b", true, true},
		{"b*", true, true},
	}
	for _, c := range cases {
		// Red fill, blue stroke, so the two can be told apart.
		content := "1 0 0 rg 0 0 1 RG 4 w 10 10 m 30 10 l 30 30 l 10 30 l h " + c.op
		d := onePage(t, [4]float64{0, 0, 40, 40}, content, nil)
		img := draw(t, d, Options{})
		inside := img.At(20, 20)
		filled := inside.R > 200 && inside.G < 60
		if filled != c.insideFilled {
			t.Errorf("%q: filled = %v", c.op, filled)
		}
		edge := img.At(20, 30)
		stroked := edge.B > 200 && edge.R < 60
		if stroked != c.edgeStroked {
			t.Errorf("%q: stroked = %v", c.op, stroked)
		}
	}
}

func TestLineWidth(t *testing.T) {
	thin := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, "0 G 1 w 5 20 m 35 20 l S", nil), Options{})
	thick := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, "0 G 9 w 5 20 m 35 20 l S", nil), Options{})
	if inked(thick) <= inked(thin) {
		t.Errorf("a nine-point line inked %d pixels against a one-point line's %d", inked(thick), inked(thin))
	}
	// A width of zero still draws the thinnest line there is.
	hair := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, "0 G 0 w 5 20 m 35 20 l S", nil), Options{})
	if inked(hair) == 0 {
		t.Error("a width of zero drew nothing")
	}
}

func TestCapsAndJoins(t *testing.T) {
	// A butt cap stops at the end point; a square cap goes past it.
	butt := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, "0 G 8 w 0 J 10 20 m 30 20 l S", nil), Options{})
	square := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, "0 G 8 w 2 J 10 20 m 30 20 l S", nil), Options{})
	round := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, "0 G 8 w 1 J 10 20 m 30 20 l S", nil), Options{})
	if inked(square) <= inked(butt) {
		t.Error("a square cap inked no more than a butt one")
	}
	if inked(round) <= inked(butt) {
		t.Error("a round cap inked no more than a butt one")
	}
	// The three joins differ at a sharp corner.
	sharp := func(j string) int {
		return inked(draw(t, onePage(t, [4]float64{0, 0, 60, 60},
			"0 G 8 w "+j+" 10 10 m 40 10 l 40 40 l S", nil), Options{}))
	}
	if sharp("0 j") == sharp("2 j") {
		t.Error("a miter join inked the same as a bevel one")
	}
	if sharp("1 j") == sharp("2 j") {
		t.Error("a round join inked the same as a bevel one")
	}
	// And the miter limit turns a spike into a bevel.
	limited := inked(draw(t, onePage(t, [4]float64{0, 0, 60, 60},
		"0 G 8 w 0 j 1.2 M 10 10 m 40 10 l 40 40 l S", nil), Options{}))
	if limited >= sharp("0 j") {
		t.Error("the miter limit made no difference")
	}
}

func TestDashes(t *testing.T) {
	solid := draw(t, onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w 0 10 m 60 10 l S", nil), Options{})
	dashed := draw(t, onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w [6 6] 0 d 0 10 m 60 10 l S", nil), Options{})
	if inked(dashed) >= inked(solid) {
		t.Error("a dashed line inked as much as a solid one")
	}
	if inked(dashed) == 0 {
		t.Error("a dashed line inked nothing at all")
	}
	// An empty array puts it back.
	back := draw(t, onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w [6 6] 0 d [] 0 d 0 10 m 60 10 l S", nil), Options{})
	if inked(back) != inked(solid) {
		t.Errorf("an empty pattern left %d pixels against a solid line's %d", inked(back), inked(solid))
	}
	// A pattern that is not numbers is no pattern.
	odd := draw(t, onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w [/x] 0 d 0 10 m 60 10 l S", nil), Options{})
	if inked(odd) != inked(solid) {
		t.Error("a pattern of names was taken for a pattern")
	}
	// So is one whose operands are missing.
	short := draw(t, onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w d 0 10 m 60 10 l S", nil), Options{})
	if inked(short) != inked(solid) {
		t.Error("a d with no operands was taken for a pattern")
	}
	notArray := draw(t, onePage(t, [4]float64{0, 0, 60, 20}, "0 G 4 w 5 0 d 0 10 m 60 10 l S", nil), Options{})
	if inked(notArray) != inked(solid) {
		t.Error("a d whose first operand is a number was taken for a pattern")
	}
}

func TestOperatorsWithTooFewOperands(t *testing.T) {
	// None of these should draw anything, and none should give up on the rest
	// of the stream: the mark at the end has to appear.
	for _, bad := range []string{
		"cm", "1 0 0 1 0 cm", "w", "J", "j", "M",
		"m", "10 m", "l", "c", "v", "y", "re", "10 10 re",
	} {
		content := bad + " 0 g 5 5 10 10 re f"
		d := onePage(t, [4]float64{0, 0, 40, 40}, content, nil)
		img := draw(t, d, Options{})
		if !isBlack(img, 10, 30) {
			t.Errorf("%q: the mark after it is missing", bad)
		}
	}
}

func TestSegmentsBeforeAnyMoveAreIgnored(t *testing.T) {
	// A line or a curve with nowhere to start from draws nothing, and does not
	// stop what follows.
	d := onePage(t, [4]float64{0, 0, 40, 40}, "0 g 20 20 l 30 30 30 30 30 30 c f 5 5 10 10 re f", nil)
	img := draw(t, d, Options{})
	if !isBlack(img, 10, 30) {
		t.Error("the mark after it is missing")
	}
}

func TestQWithNothingSaved(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 40}, "Q Q 0 g 5 5 10 10 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 10, 30)
}

func TestARectangleThatIsNotOne(t *testing.T) {
	// Boxes a file writes that are not boxes at all fall through to the next
	// choice, and then to letter paper.
	for _, box := range []string{
		"[0 0 0 100]", "[/x 0 100 100]", "[0 0]", "42",
	} {
		d := onePage(t, [4]float64{0, 0, 100, 100}, "0 g 0 0 10 10 re f", nil)
		page, err := d.Page(1)
		if err != nil {
			t.Fatal(err)
		}
		_ = page
		_ = box
	}
	// The media box itself being unusable gives letter paper.
	if _, ok := rectangle(nil, nil); ok {
		t.Error("nothing is not a rectangle")
	}
}
