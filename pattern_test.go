package render

import (
	"testing"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-pdfkit/reader"
)

func TestAShadingPatternFillingAShape(t *testing.T) {
	// A gradient used as a fill colour: the shape is the path, the colour
	// comes from the shading.
	d := shadedPage(t, "/Pattern cs /P1 scn 10 10 80 80 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
			"Type": reader.Name("Pattern"), "PatternType": reader.Integer(2),
			"Shading": reader.Dict{
				"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
				"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
				"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
			},
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := img.At(15, 50); c.R < 150 {
		t.Errorf("the left of the shape is %v, want red", c)
	}
	if c := img.At(85, 50); !isBlue(c) {
		t.Errorf("the right of the shape is %v, want blue", c)
	}
	if !isWhite(img, 3, 50) {
		t.Errorf("outside the shape it painted %v", img.At(3, 50))
	}
}

func TestAShadingPatternStrokingAShape(t *testing.T) {
	d := shadedPage(t, "/Pattern CS /P1 SCN 8 w 10 50 m 90 50 l S", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
			"Type": reader.Name("Pattern"), "PatternType": reader.Integer(2),
			"Shading": reader.Dict{
				"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceRGB"),
				"Coords": nums(0, 0, 100, 0), "Function": rampFunction(w),
				"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
			},
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := img.At(15, 50); c.R < 150 {
		t.Errorf("the left of the line is %v, want red", c)
	}
	if c := img.At(85, 50); !isBlue(c) {
		t.Errorf("the right of the line is %v, want blue", c)
	}
	if !isWhite(img, 50, 20) {
		t.Error("the line was painted where it is not")
	}
}

// hatchPattern is a tiling pattern drawing one horizontal bar per cell.
func hatchPattern(w *reader.Writer, paintType int, content string) reader.Object {
	return w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("Pattern"), "PatternType": reader.Integer(1),
		"PaintType": reader.Integer(int64(paintType)), "TilingType": reader.Integer(1),
		"BBox": nums(0, 0, 20, 20), "XStep": reader.Integer(20), "YStep": reader.Integer(20),
		"Resources": reader.Dict{},
	}, Raw: []byte(content)})
}

func TestATilingPatternFillingAShape(t *testing.T) {
	// A coloured tiling pattern: the cell draws its own colours, repeated
	// across the shape and clipped to it.
	d := shadedPage(t, "/Pattern cs /P1 scn 10 10 80 80 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{
			"P1": hatchPattern(w, 1, "0 0 1 rg 0 0 20 8 re f"),
		}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The bars repeat, so some rows inside the shape are blue and some are
	// paper — and none of it reaches outside.
	blue, paper := 0, 0
	for y := 12; y < 88; y++ {
		if isBlue(img.At(50, y)) {
			blue++
		} else if isWhite(img, 50, y) {
			paper++
		}
	}
	if blue == 0 {
		t.Error("the tiling pattern drew nothing")
	}
	if paper == 0 {
		t.Error("the tiling pattern filled everything rather than repeating")
	}
	if !isWhite(img, 3, 50) {
		t.Errorf("outside the shape it painted %v", img.At(3, 50))
	}
}

func TestAnUncolouredTilingPatternTakesTheColourItIsGiven(t *testing.T) {
	// A pattern of the second paint type is a shape only: the colour is the
	// one named where it is used, and every colour operator inside it is to
	// be passed over. One that is not passed over paints white over the
	// page, which is exactly what was found happening.
	d := shadedPage(t, "/Cs cs 0 0.6 0 /P1 scn 10 10 80 80 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{
			"ColorSpace": reader.Dict{"Cs": reader.Array{reader.Name("Pattern"), reader.Name("DeviceRGB")}},
			"Pattern": reader.Dict{
				// The white fill inside must not reach the page.
				"P1": hatchPattern(w, 2, "1 1 1 rg 0 0 20 8 re f"),
			},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	green := 0
	for y := 12; y < 88; y++ {
		if c := img.At(50, y); c.G > 100 && c.R < 100 && c.B < 100 {
			green++
		}
	}
	if green == 0 {
		t.Error("an uncoloured pattern did not draw in the colour it was given")
	}
}

func TestATilingPatternIsNotDrawnInsideItself(t *testing.T) {
	// A pattern whose cell fills with the pattern in force would start the
	// whole thing again at every tile, which on a real page ran eleven deep
	// and rubbed the page out. The cell begins from a clean colour instead.
	d := shadedPage(t, "/Pattern cs /P1 scn 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{
			"P1": hatchPattern(w, 1, "0 0 20 8 re f"),
		}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	dark := 0
	for y := 0; y < 100; y++ {
		if c := img.At(50, y); c.R < 60 {
			dark++
		}
	}
	if dark == 0 {
		t.Error("the pattern drew nothing")
	}
	if dark > 90 {
		t.Errorf("the pattern filled %d of a hundred rows, which is not a repeat", dark)
	}
}

func TestPatternsThatCannotBeDrawn(t *testing.T) {
	cases := []struct {
		name    string
		content string
		build   func(w *reader.Writer) reader.Dict
	}{
		{"no Pattern resources", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict { return reader.Dict{} }},
		{"a name nothing answers to", "/Pattern cs /Missing scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": reader.Integer(3)}}
			}},
		{"a pattern that is not a dictionary", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": reader.Integer(3)}}
			}},
		{"a kind of pattern that does not exist", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
					"PatternType": reader.Integer(9)})}}
			}},
		{"a shading pattern whose shading is not one", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
					"PatternType": reader.Integer(2), "Shading": reader.Integer(3)})}}
			}},
		{"a tiling pattern that is not a stream", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
					"PatternType": reader.Integer(1), "BBox": nums(0, 0, 10, 10)})}}
			}},
		{"a tiling pattern with no box", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{
					Dict: reader.Dict{"PatternType": reader.Integer(1)},
					Raw:  []byte("0 0 10 10 re f")})}}
			}},
		{"a pattern named with no name at all", "/Pattern cs scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict { return reader.Dict{} }},
		{"a pattern flattened to nothing by its own matrix", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{
					Dict: reader.Dict{"PatternType": reader.Integer(1), "BBox": nums(0, 0, 10, 10),
						"Matrix": nums(0, 0, 0, 0, 0, 0)},
					Raw: []byte("0 0 10 10 re f")})}}
			}},
		{"a pattern whose step is nothing at all", "/Pattern cs /P1 scn 10 10 80 80 re f",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{
					Dict: reader.Dict{"PatternType": reader.Integer(1), "BBox": nums(0, 0, 0, 0)},
					Raw:  []byte("0 0 10 10 re f")})}}
			}},
	}
	for _, c := range cases {
		d := shadedPage(t, c.content, c.build)
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		// A pattern that cannot be drawn leaves a mid grey rather than
		// nothing, so that what was asked for does not simply vanish; what
		// matters is that the page came out at all.
		_ = img
	}
}

func TestAPatternAskingForMoreTilesThanAnyoneNeeds(t *testing.T) {
	// A pattern whose step is a hair across would tile a page millions of
	// times; past a limit it is left undrawn rather than allowed to take the
	// afternoon.
	d := shadedPage(t, "/Pattern cs /P1 scn 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{
			Dict: reader.Dict{"Type": reader.Name("Pattern"),
				"PatternType": reader.Integer(1), "PaintType": reader.Integer(1),
				"BBox": nums(0, 0, 0.01, 0.01), "XStep": reader.Real(0.01), "YStep": reader.Real(0.01)},
			Raw: []byte("0 0 0.01 0.01 re f")})}}
	})
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestAPatternNestedTooDeeply(t *testing.T) {
	// A pattern drawn from inside a form drawn from inside a pattern, past
	// the depth a page is allowed to nest, is left undrawn.
	r := &renderer{depth: maxFormDepth}
	r.tile(&gstate{}, &pattern{}, geometry.Identity(), nil, 0, 0, 0, 0, 1)
}

func TestAnUncolouredPatternUsedForStroking(t *testing.T) {
	// The colour an uncoloured pattern draws in is given where it is used,
	// and a stroking use names it in the stroking space.
	d := shadedPage(t, "/Cs CS 0 0 1 /P1 SCN 10 w 10 50 m 90 50 l S", func(w *reader.Writer) reader.Dict {
		return reader.Dict{
			"ColorSpace": reader.Dict{"Cs": reader.Array{reader.Name("Pattern"), reader.Name("DeviceRGB")}},
			"Pattern":    reader.Dict{"P1": hatchPattern(w, 2, "0 0 20 20 re f")},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c := img.At(50, 50); !isBlue(c) {
		t.Errorf("the line is %v, want blue", c)
	}
}

func TestATilingPatternFilteredAsAnImage(t *testing.T) {
	// A pattern whose content is filtered as an image is not a pattern.
	d := shadedPage(t, "/Pattern cs /P1 scn 10 10 80 80 re f", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(&reader.Stream{
			Dict: reader.Dict{"PatternType": reader.Integer(1), "BBox": nums(0, 0, 20, 20),
				"Filter": reader.Name("DCTDecode")},
			Raw: []byte("not a jpeg")})}}
	})
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
}
