package render

import (
	"image/color"
	"testing"

	"github.com/go-pdfkit/reader"
)

// shadedPage builds a page whose resources hold what build puts there and
// whose content is what the test wants drawn.
func shadedPage(t *testing.T, content string, build func(w *reader.Writer) reader.Dict) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	resources := build(w)
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": resources,
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// rampFunction is a function from red to blue, which makes a gradient easy to
// read off a page.
func rampFunction(w *reader.Writer) reader.Object {
	return w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1),
		"C0": nums(1, 0, 0), "C1": nums(0, 0, 1), "N": reader.Integer(1)})
}

func TestAnAxialShadingPaintedByItself(t *testing.T) {
	// The sh operator paints a gradient over everything the clip allows.
	// Left should be red and right blue, which is what says the axis was
	// read the right way round.
	d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords":   nums(0, 0, 100, 0),
			"Function": rampFunction(w),
			"Extend":   reader.Array{reader.Bool(true), reader.Bool(true)},
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	left, right := img.At(5, 50), img.At(94, 50)
	if left.R < 200 || left.B > 60 {
		t.Errorf("the left is %v, want red", left)
	}
	if right.B < 200 || right.R > 60 {
		t.Errorf("the right is %v, want blue", right)
	}
	middle := img.At(50, 50)
	if middle.R < 80 || middle.R > 180 || middle.B < 80 || middle.B > 180 {
		t.Errorf("the middle is %v, want halfway", middle)
	}
}

func TestAnAxialShadingThatDoesNotReachTheEdges(t *testing.T) {
	// Without Extend the shading paints only between its two ends, and the
	// paper on either side is left alone.
	d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(40, 0, 60, 0), "Function": rampFunction(w),
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 5, 50) {
		t.Errorf("outside the axis the paper is %v", img.At(5, 50))
	}
	if isWhite(img, 50, 50) {
		t.Error("between the ends nothing was painted")
	}
	// A background is what is painted where the shading itself says nothing.
	d = shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(40, 0, 60, 0), "Function": rampFunction(w),
			"Background": nums(0, 1, 0),
		})}}
	})
	img, err = Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := img.At(5, 50); c.G < 200 || c.R > 60 {
		t.Errorf("outside the axis it is %v, want the background green", c)
	}
}

func TestARadialShading(t *testing.T) {
	// Two circles, one inside the other: the middle takes the first colour
	// and the rim the second.
	d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(3), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(50, 50, 0, 50, 50, 45), "Function": rampFunction(w),
			"Extend": reader.Array{reader.Bool(false), reader.Bool(false)},
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := img.At(50, 50); c.R < 200 {
		t.Errorf("the middle is %v, want red", c)
	}
	if c := img.At(50, 8); c.B < 180 {
		t.Errorf("the rim is %v, want blue", c)
	}
	if !isWhite(img, 2, 2) {
		t.Errorf("the corner outside the circle is %v", img.At(2, 2))
	}
}

func TestARadialShadingOfTwoCirclesSideBySide(t *testing.T) {
	// Circles that do not contain one another make a cone rather than a
	// disc, which is the case where the quadratic has two roots.
	d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(3), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(30, 50, 10, 70, 50, 20), "Function": rampFunction(w),
			"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if isWhite(img, 50, 50) {
		t.Error("nothing was painted between the two circles")
	}
	// And one where the radii shrink to nothing: a single point cannot be
	// solved for, and nothing is painted rather than something wrong.
	d = shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(3), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(50, 50, 0, 50, 50, 0), "Function": rampFunction(w),
		})}}
	})
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestAFunctionBasedShading(t *testing.T) {
	// The first kind: the colour is simply a function of where you are. The
	// corpus has none, but the format has it and the machinery is the same.
	d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		fn := w.Add(&reader.Stream{Dict: reader.Dict{
			"FunctionType": reader.Integer(4), "Domain": nums(0, 1, 0, 1),
			"Range": nums(0, 1, 0, 1, 0, 1),
		}, Raw: []byte("{ pop dup 1 exch sub 0 }")})
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(1), "ColorSpace": reader.Name("DeviceRGB"),
			"Domain": nums(0, 1, 0, 1), "Function": fn,
			"Matrix": nums(100, 0, 0, 100, 0, 0),
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if isWhite(img, 50, 50) {
		t.Error("a function-based shading painted nothing")
	}
	// Outside its domain it paints nothing.
	d = shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		fn := w.Add(&reader.Stream{Dict: reader.Dict{
			"FunctionType": reader.Integer(4), "Domain": nums(0, 1, 0, 1),
			"Range": nums(0, 1, 0, 1, 0, 1),
		}, Raw: []byte("{ pop dup 1 exch sub 0 }")})
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(1), "ColorSpace": reader.Name("DeviceRGB"),
			"Domain": nums(0, 0.1, 0, 0.1), "Function": fn,
		})}}
	})
	img, err = Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 50, 50) {
		t.Errorf("outside the domain it painted %v", img.At(50, 50))
	}
}

func TestAShadingWithABoxAroundIt(t *testing.T) {
	d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
			"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
			"BBox":   nums(20, 20, 80, 80),
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 5, 50) {
		t.Errorf("outside the box it painted %v", img.At(5, 50))
	}
	if isWhite(img, 50, 50) {
		t.Error("inside the box it painted nothing")
	}
}

func TestShadingsThatCannotBeDrawn(t *testing.T) {
	cases := []struct {
		name string
		dict func(w *reader.Writer) reader.Object
	}{
		{"not a dictionary", func(w *reader.Writer) reader.Object { return reader.Integer(3) }},
		{"no shading type", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ColorSpace": reader.Name("DeviceRGB")})
		}},
		{"a colour space that is a pattern", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("Pattern"), "Coords": nums(0, 0, 1, 0)})
		}},
		{"an axial shading with no function", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(0, 0, 1, 0)})
		}},
		{"an axial shading with no coordinates", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "Function": rampFunction(w)})
		}},
		{"a radial shading with too few coordinates", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(3),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(0, 0, 1),
				"Function": rampFunction(w)})
		}},
		{"a function giving the wrong number of colours", func(w *reader.Writer) reader.Object {
			one := w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1)})
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(0, 0, 1, 0),
				"Function": one})
		}},
		{"a mesh, which is a wave still to come", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{"ShadingType": reader.Integer(4),
				"ColorSpace": reader.Name("DeviceRGB"), "Function": rampFunction(w)},
				Raw: []byte{0}})
		}},
		{"a function-based shading whose matrix flattens everything", func(w *reader.Writer) reader.Object {
			fn := w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(4), "Domain": nums(0, 1, 0, 1),
				"Range": nums(0, 1, 0, 1, 0, 1)}, Raw: []byte("{ pop dup dup }")})
			return w.Add(reader.Dict{"ShadingType": reader.Integer(1),
				"ColorSpace": reader.Name("DeviceRGB"), "Function": fn,
				"Matrix": nums(0, 0, 0, 0, 0, 0)})
		}},
	}
	for _, c := range cases {
		d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Shading": reader.Dict{"S1": c.dict(w)}}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !isWhite(img, 50, 50) {
			t.Errorf("%s: something was painted (%v)", c.name, img.At(50, 50))
		}
	}
}

func TestTheShadingOperatorWithNothingToDraw(t *testing.T) {
	// Every way sh can be asked for something that is not there.
	for _, content := range []string{
		"/S1 sh",      // no Shading resource at all
		"sh",          // no name
		"7 sh",        // a name that is not one
		"/Missing sh", // a name nothing answers to
	} {
		d := shadedPage(t, content, func(w *reader.Writer) reader.Dict { return reader.Dict{} })
		if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
			t.Fatalf("%q: %v", content, err)
		}
	}
	d := shadedPage(t, "/Missing sh", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": reader.Integer(1)}}
	})
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
}

// isBlue reports whether a colour is the blue end of the test ramp.
func isBlue(c color.RGBA) bool { return c.B > 150 && c.R < 100 }

func TestTheEdgesOfAShading(t *testing.T) {
	// The cases a real page reaches rarely and a gradient goes wrong in
	// quietly: an axis of no length, a domain other than nought to one, a
	// radial family of circles that grows exactly as fast as it moves, and
	// what happens beyond either end.
	cases := []struct {
		name  string
		dict  func(w *reader.Writer) reader.Object
		at    [2]int
		white bool
	}{
		{"an axis with both ends in the same place", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(50, 50, 50, 50),
				"Function": rampFunction(w)})
		}, [2]int{50, 50}, false},
		{"an axis extended past both ends", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(45, 0, 55, 0),
				"Function": rampFunction(w),
				"Extend":   reader.Array{reader.Bool(true), reader.Bool(true)}})
		}, [2]int{5, 50}, false},
		{"a domain that is not nought to one", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(0, 0, 100, 0),
				"Domain": nums(0.25, 0.75), "Function": rampFunction(w),
				"Extend": reader.Array{reader.Bool(true), reader.Bool(true)}})
		}, [2]int{5, 50}, false},
		{"circles that grow as fast as they move", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(3),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(0, 50, 0, 60, 50, 60),
				"Function": rampFunction(w),
				"Extend":   reader.Array{reader.Bool(true), reader.Bool(true)}})
		}, [2]int{50, 50}, false},
		{"a radial family that reaches nowhere near a corner", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"ShadingType": reader.Integer(3),
				"ColorSpace": reader.Name("DeviceRGB"), "Coords": nums(50, 50, 1, 50, 50, 5),
				"Function": rampFunction(w)})
		}, [2]int{2, 2}, true},
		{"a function-based shading with no domain of its own", func(w *reader.Writer) reader.Object {
			fn := w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(4), "Domain": nums(0, 1, 0, 1),
				"Range": nums(0, 1, 0, 1, 0, 1)}, Raw: []byte("{ pop dup dup }")})
			return w.Add(reader.Dict{"ShadingType": reader.Integer(1),
				"ColorSpace": reader.Name("DeviceRGB"), "Function": fn})
		}, [2]int{50, 99}, true},
	}
	for _, c := range cases {
		d := shadedPage(t, "q 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Shading": reader.Dict{"S1": c.dict(w)}}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := isWhite(img, c.at[0], c.at[1]); got != c.white {
			t.Errorf("%s: at %v it is %v, want white = %v", c.name, c.at, img.At(c.at[0], c.at[1]), c.white)
		}
	}
}

func TestAShadingPaintedThroughNothing(t *testing.T) {
	// A shading painted with the page made completely see-through puts
	// nothing on it, and one painted through a clip of nothing likewise.
	d := shadedPage(t, "q /GS gs 0 0 100 100 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{
			"ExtGState": reader.Dict{"GS": reader.Dict{"ca": reader.Integer(0)}},
			"Shading": reader.Dict{"S1": w.Add(reader.Dict{
				"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
				"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
				"Extend": reader.Array{reader.Bool(true), reader.Bool(true)}})},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 50, 50) {
		t.Errorf("a shading with no opacity painted %v", img.At(50, 50))
	}
	d = shadedPage(t, "q 0 0 0 0 re W n /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
			"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
			"Extend": reader.Array{reader.Bool(true), reader.Bool(true)}})}}
	})
	img, err = Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 50, 50) {
		t.Errorf("a shading clipped to nothing painted %v", img.At(50, 50))
	}
}
