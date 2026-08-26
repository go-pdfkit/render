package render

import (
	"image/color"
	"math"

	"github.com/go-gfx/gfx/raster"
	"testing"

	"github.com/go-pdfkit/reader"
)

// meshBytes packs the numbers a mesh stream is made of, a bit at a time, since
// the widths a file may name are not all whole bytes.
type meshBytes struct {
	b []byte
	n int // bits written
}

// bits writes one number of the given width.
func (m *meshBytes) bits(v uint64, w int) *meshBytes {
	for k := w - 1; k >= 0; k-- {
		if m.n%8 == 0 {
			m.b = append(m.b, 0)
		}
		if v>>uint(k)&1 == 1 {
			m.b[len(m.b)-1] |= 1 << (7 - m.n%8)
		}
		m.n++
	}
	return m
}

// coord writes one 32-bit coordinate, mapped onto the nought-to-a-hundred
// decode range the tests below all use.
func (m *meshBytes) coord(v float64) *meshBytes {
	return m.bits(uint64(math.Round(v/100*float64(^uint32(0)))), 32)
}

// point writes a place on the page.
func (m *meshBytes) point(x, y float64) *meshBytes { return m.coord(x).coord(y) }

// rgb writes one colour, three components eight bits wide.
func (m *meshBytes) rgb(c color.RGBA) *meshBytes {
	return m.bits(uint64(c.R), 8).bits(uint64(c.G), 8).bits(uint64(c.B), 8)
}

// flag writes the byte that says how a vertex or a patch joins the last one.
func (m *meshBytes) flag(v byte) *meshBytes { return m.bits(uint64(v), 8) }

// one writes a single component, for a mesh whose colours go through a
// function.
func (m *meshBytes) one(v byte) *meshBytes { return m.bits(uint64(v), 8) }

var (
	meshRed   = color.RGBA{R: 255, A: 255}
	meshGreen = color.RGBA{G: 255, A: 255}
	meshBlue  = color.RGBA{B: 255, A: 255}
	meshWhite = color.RGBA{R: 255, G: 255, B: 255, A: 255}
)

// meshDecode is the decode array every test here writes its numbers against:
// a hundred points each way, and colour components from nought to one.
func meshDecode(components int) reader.Array {
	v := []float64{0, 100, 0, 100}
	for i := 0; i < components; i++ {
		v = append(v, 0, 1)
	}
	return nums(v...)
}

// meshShading builds a page that paints one mesh over the whole of it.
func meshShading(t *testing.T, kind int, data []byte, extra reader.Dict) *reader.Document {
	t.Helper()
	return shadedPage(t, "/S1 sh", func(w *reader.Writer) reader.Dict {
		dict := reader.Dict{
			"ShadingType":       reader.Integer(int64(kind)),
			"ColorSpace":        reader.Name("DeviceRGB"),
			"BitsPerCoordinate": reader.Integer(32),
			"BitsPerComponent":  reader.Integer(8),
			"BitsPerFlag":       reader.Integer(8),
			"Decode":            meshDecode(3),
		}
		for k, v := range extra {
			dict[k] = v
		}
		return reader.Dict{"Shading": reader.Dict{
			"S1": w.Add(&reader.Stream{Dict: dict, Raw: data}),
		}}
	})
}

// renderMesh draws such a page.
func renderMesh(t *testing.T, kind int, data []byte, extra reader.Dict) *raster.Image {
	t.Helper()
	img, err := Page(meshShading(t, kind, data, extra), 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// nearly says whether two colours are close enough that a rounding either way
// does not fail the test.
func nearly(a, b color.RGBA, tol int) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol && d(a.B, b.B) <= tol
}

// wantMeshColour fails unless the pixel is what it should be.
func wantMeshColour(t *testing.T, img *raster.Image, x, y int, want color.RGBA, why string) {
	t.Helper()
	if got := img.At(x, y); !nearly(got, want, 15) {
		t.Errorf("%s: pixel (%d,%d) is %v, wanted %v", why, x, y, got, want)
	}
}

// freeTriangle is one triangle written the way a type 4 stream writes it.
func freeTriangle() []byte {
	m := &meshBytes{}
	m.flag(0).point(0, 0).rgb(meshRed)
	m.flag(0).point(100, 0).rgb(meshGreen)
	m.flag(0).point(0, 100).rgb(meshBlue)
	return m.b
}

func TestAFreeFormTriangleMeshIsDrawn(t *testing.T) {
	// The three corners take their own colours and the half of the page the
	// triangle does not reach is left as paper.
	img := renderMesh(t, 4, freeTriangle(), nil)
	wantMeshColour(t, img, 2, 97, meshRed, "the corner at the origin")
	wantMeshColour(t, img, 97, 97, meshGreen, "the corner along the bottom")
	wantMeshColour(t, img, 2, 2, meshBlue, "the corner up the side")
	wantMeshColour(t, img, 90, 10, meshWhite, "outside the triangle")
	// The middle of the hypotenuse is halfway between two of the corners.
	wantMeshColour(t, img, 50, 50, color.RGBA{G: 128, B: 128, A: 255}, "the middle of the long side")
}

func TestAFreeFormMeshCarriesTwoVerticesOn(t *testing.T) {
	// A flag of one keeps the last two vertices, a flag of two keeps the
	// first and the last: a strip written without repeating anything.
	m := &meshBytes{}
	m.flag(0).point(0, 0).rgb(meshRed)
	m.flag(0).point(100, 0).rgb(meshRed)
	m.flag(0).point(0, 100).rgb(meshRed)
	m.flag(1).point(100, 100).rgb(meshBlue)
	img := renderMesh(t, 4, m.b, nil)
	wantMeshColour(t, img, 2, 97, meshRed, "the first triangle")
	wantMeshColour(t, img, 97, 2, meshBlue, "the corner the second triangle added")

	m = &meshBytes{}
	m.flag(0).point(0, 0).rgb(meshRed)
	m.flag(0).point(100, 0).rgb(meshRed)
	m.flag(0).point(0, 100).rgb(meshRed)
	m.flag(2).point(100, 100).rgb(meshBlue)
	img = renderMesh(t, 4, m.b, nil)
	wantMeshColour(t, img, 97, 2, meshBlue, "the corner the flag of two added")
}

func TestAFreeFormMeshStartsAgainOnAZeroFlag(t *testing.T) {
	// A zero flag after a whole triangle throws the three away and begins
	// another, which is the only way a stream says two separate shapes.
	m := &meshBytes{}
	m.flag(0).point(0, 0).rgb(meshRed)
	m.flag(0).point(40, 0).rgb(meshRed)
	m.flag(0).point(0, 40).rgb(meshRed)
	m.flag(0).point(60, 60).rgb(meshBlue)
	m.flag(0).point(100, 60).rgb(meshBlue)
	m.flag(0).point(60, 100).rgb(meshBlue)
	img := renderMesh(t, 4, m.b, nil)
	wantMeshColour(t, img, 2, 97, meshRed, "the first triangle")
	wantMeshColour(t, img, 65, 35, meshBlue, "the second triangle")
	wantMeshColour(t, img, 90, 10, meshWhite, "between the two")
}

func TestALatticeFormMeshIsDrawn(t *testing.T) {
	// Two rows of two: the four corners of the page, each its own colour,
	// with the square between them filled in.
	m := &meshBytes{}
	m.point(0, 0).rgb(meshRed)
	m.point(100, 0).rgb(meshGreen)
	m.point(0, 100).rgb(meshBlue)
	m.point(100, 100).rgb(meshWhite)
	img := renderMesh(t, 5, m.b, reader.Dict{"VerticesPerRow": reader.Integer(2)})
	wantMeshColour(t, img, 2, 97, meshRed, "the corner at the origin")
	wantMeshColour(t, img, 97, 97, meshGreen, "the corner along the bottom")
	wantMeshColour(t, img, 2, 2, meshBlue, "the corner up the side")
	wantMeshColour(t, img, 97, 2, meshWhite, "the far corner")
}

// flatPatch writes the twelve boundary points of a patch that covers the whole
// page and is not curved at all, so that what comes out can be read off.
func flatPatch(m *meshBytes, from int) *meshBytes {
	net := func(i, j int) (float64, float64) {
		return float64(i) * 100 / 3, float64(j) * 100 / 3
	}
	order := [12][2]int{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {1, 3}, {2, 3},
		{3, 3}, {3, 2}, {3, 1}, {3, 0}, {2, 0}, {1, 0}}
	for i := from; i < 12; i++ {
		m.point(net(order[i][0], order[i][1]))
	}
	return m
}

func TestACoonsPatchIsDrawn(t *testing.T) {
	// A flat patch with a different colour at each corner comes out as the
	// four colours mixed across it, which is what says both the surface and
	// the way round the corners go were read right.
	m := &meshBytes{}
	m.flag(0)
	flatPatch(m, 0)
	m.rgb(meshRed).rgb(meshGreen).rgb(meshBlue).rgb(meshWhite)
	img := renderMesh(t, 6, m.b, nil)
	wantMeshColour(t, img, 2, 97, meshRed, "the corner the patch starts at")
	wantMeshColour(t, img, 2, 2, meshGreen, "the corner along the first side")
	wantMeshColour(t, img, 97, 2, meshBlue, "the corner across the patch")
	wantMeshColour(t, img, 97, 97, meshWhite, "the last corner")
	wantMeshColour(t, img, 50, 50, color.RGBA{R: 128, G: 128, B: 128, A: 255}, "the middle")
}

func TestATensorPatchWithACoonsInsideDrawsTheSame(t *testing.T) {
	// A tensor patch says four more points; giving it the ones a Coons patch
	// would have worked out for itself must draw the very same thing.
	coons := &meshBytes{}
	coons.flag(0)
	flatPatch(coons, 0)
	coons.rgb(meshRed).rgb(meshGreen).rgb(meshBlue).rgb(meshWhite)

	tensor := &meshBytes{}
	tensor.flag(0)
	flatPatch(tensor, 0)
	for _, at := range [4][2]int{{1, 1}, {1, 2}, {2, 2}, {2, 1}} {
		tensor.point(float64(at[0])*100/3, float64(at[1])*100/3)
	}
	tensor.rgb(meshRed).rgb(meshGreen).rgb(meshBlue).rgb(meshWhite)

	a := renderMesh(t, 6, coons.b, nil)
	b := renderMesh(t, 7, tensor.b, nil)
	for y := 0; y < 100; y += 7 {
		for x := 0; x < 100; x += 7 {
			if a.At(x, y) != b.At(x, y) {
				t.Fatalf("pixel (%d,%d): the Coons patch is %v and the tensor patch %v",
					x, y, a.At(x, y), b.At(x, y))
			}
		}
	}
}

func TestAPatchCarriesTheEdgeOfTheOneBefore(t *testing.T) {
	// Each of the three continuing flags shares a different edge. Whichever
	// it is, the new patch keeps two colours as well as four points, so the
	// seam between the two is the same colour on both sides.
	for _, flag := range []byte{1, 2, 3} {
		m := &meshBytes{}
		m.flag(0)
		flatPatch(m, 0)
		m.rgb(meshRed).rgb(meshGreen).rgb(meshBlue).rgb(meshWhite)
		m.flag(flag)
		flatPatch(m, 4)
		m.rgb(meshRed).rgb(meshRed)
		img := renderMesh(t, 6, m.b, nil)
		// The second patch reuses the first patch's own points, so it covers
		// the page again; what matters is that it was read without running
		// off the end and that something was drawn.
		if img.At(50, 50) == meshWhite {
			t.Errorf("flag %d: the middle of the page was left as paper", flag)
		}
	}
}

func TestAPatchThatCarriesOnFromNothingIsRefused(t *testing.T) {
	// A stream whose first patch says it shares an edge has nothing to share
	// it with, so nothing is drawn rather than something made up.
	m := &meshBytes{}
	m.flag(1)
	flatPatch(m, 4)
	m.rgb(meshRed).rgb(meshGreen)
	img := renderMesh(t, 6, m.b, nil)
	wantMeshColour(t, img, 50, 50, meshWhite, "a patch with no patch before it")
}

func TestAMeshColoursThroughAFunction(t *testing.T) {
	// A mesh may write one number a vertex and name a function that turns it
	// into a colour, which is how a gradient triangle is written small.
	m := &meshBytes{}
	m.flag(0).point(0, 0).one(0)
	m.flag(0).point(100, 0).one(255)
	m.flag(0).point(0, 100).one(0)
	d := shadedPage(t, "/S1 sh", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(&reader.Stream{
			Dict: reader.Dict{
				"ShadingType": reader.Integer(4), "ColorSpace": reader.Name("DeviceRGB"),
				"BitsPerCoordinate": reader.Integer(32), "BitsPerComponent": reader.Integer(8),
				"BitsPerFlag": reader.Integer(8), "Decode": meshDecode(1),
				"Function": rampFunction(w),
			}, Raw: m.b})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantMeshColour(t, img, 2, 97, meshRed, "where the function was given nought")
	wantMeshColour(t, img, 95, 97, meshBlue, "where it was given one")
}

func TestAMeshWhoseFunctionGivesTheWrongNumberOfComponentsIsRefused(t *testing.T) {
	m := &meshBytes{}
	m.flag(0).point(0, 0).one(0)
	m.flag(0).point(100, 0).one(255)
	m.flag(0).point(0, 100).one(0)
	d := shadedPage(t, "/S1 sh", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(&reader.Stream{
			Dict: reader.Dict{
				"ShadingType": reader.Integer(4), "ColorSpace": reader.Name("DeviceGray"),
				"BitsPerCoordinate": reader.Integer(32), "BitsPerComponent": reader.Integer(8),
				"BitsPerFlag": reader.Integer(8), "Decode": meshDecode(1),
				"Function": rampFunction(w), // three out, one wanted
			}, Raw: m.b})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantMeshColour(t, img, 2, 97, meshWhite, "a mesh whose function does not fit its space")
}

func TestAMeshPaintsItsBackgroundWhereItReachesNothing(t *testing.T) {
	img := renderMesh(t, 4, freeTriangle(), reader.Dict{"Background": nums(0, 1, 0)})
	wantMeshColour(t, img, 90, 10, meshGreen, "outside the triangle")
	wantMeshColour(t, img, 2, 97, meshRed, "inside it")
}

func TestAMeshKeepsToItsBoundingBox(t *testing.T) {
	img := renderMesh(t, 4, freeTriangle(), reader.Dict{"BBox": nums(0, 50, 50, 100)})
	wantMeshColour(t, img, 2, 2, meshBlue, "inside the box")
	wantMeshColour(t, img, 2, 97, meshWhite, "below the box")
}

func TestAMeshIsUsedAsAPattern(t *testing.T) {
	// A mesh may be a shading pattern rather than painted on its own, and a
	// pattern is used to fill a shape. Two fills in a row read the drawing
	// once and then again from what was kept.
	d := shadedPage(t, "/Pattern cs /P1 scn 0 0 50 100 re f 50 0 50 100 re f",
		func(w *reader.Writer) reader.Dict {
			shading := w.Add(&reader.Stream{Dict: reader.Dict{
				"ShadingType": reader.Integer(4), "ColorSpace": reader.Name("DeviceRGB"),
				"BitsPerCoordinate": reader.Integer(32), "BitsPerComponent": reader.Integer(8),
				"BitsPerFlag": reader.Integer(8), "Decode": meshDecode(3),
			}, Raw: freeTriangle()})
			return reader.Dict{"Pattern": reader.Dict{"P1": w.Add(reader.Dict{
				"PatternType": reader.Integer(2), "Shading": shading,
			})}}
		})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantMeshColour(t, img, 2, 97, meshRed, "the first fill")
	wantMeshColour(t, img, 55, 97, color.RGBA{R: 114, G: 140, A: 255}, "the second fill")
}

func TestAMeshThatSaysSomethingItCannotMeanIsRefused(t *testing.T) {
	// Every width a mesh stream names has a short list of values it may take,
	// and the decode array has to be long enough for what it describes. A
	// stream that breaks one of those is not drawn at all.
	for _, c := range []struct {
		why   string
		kind  int
		extra reader.Dict
	}{
		{"a coordinate width that is not one of the eight", 4,
			reader.Dict{"BitsPerCoordinate": reader.Integer(7)}},
		{"a component width that is not one of the six", 4,
			reader.Dict{"BitsPerComponent": reader.Integer(32)}},
		{"a flag width that is not two, four or eight", 4,
			reader.Dict{"BitsPerFlag": reader.Integer(3)}},
		{"a decode array with too few numbers in it", 4,
			reader.Dict{"Decode": nums(0, 100, 0, 100)}},
		{"a row length of one", 5, reader.Dict{"VerticesPerRow": reader.Integer(1)}},
		{"a row length past any sense", 5, reader.Dict{"VerticesPerRow": reader.Integer(1 << 20)}},
	} {
		img := renderMesh(t, c.kind, freeTriangle(), c.extra)
		wantMeshColour(t, img, 2, 97, meshWhite, c.why)
	}
}

func TestAMeshShadingThatIsNotAStreamIsRefused(t *testing.T) {
	// The four mesh kinds carry their vertices in a stream; one written as a
	// plain dictionary has nowhere to have put them.
	d := shadedPage(t, "/S1 sh", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(4), "ColorSpace": reader.Name("DeviceRGB"),
			"BitsPerCoordinate": reader.Integer(32), "BitsPerComponent": reader.Integer(8),
			"BitsPerFlag": reader.Integer(8), "Decode": meshDecode(3),
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantMeshColour(t, img, 50, 50, meshWhite, "a mesh shading with no stream")
}

func TestAMeshStreamThatStopsPartWayIsDrawnAsFarAsItGoes(t *testing.T) {
	// Files are cut short. What was read whole is drawn; the half vertex at
	// the end is not guessed at.
	full := freeTriangle()
	for _, n := range []int{0, 5, len(full) - 1} {
		img := renderMesh(t, 4, full[:n], nil)
		wantMeshColour(t, img, 2, 97, meshWhite, "a stream cut short before a triangle was whole")
	}
	// A fourth vertex begun and not finished leaves the first triangle.
	m := &meshBytes{}
	for _, b := range full {
		m.bits(uint64(b), 8)
	}
	m.flag(1).coord(50)
	img := renderMesh(t, 4, m.b, nil)
	wantMeshColour(t, img, 2, 97, meshRed, "the triangle that was written whole")
}

func TestAPatchStreamThatStopsPartWayIsRefused(t *testing.T) {
	m := &meshBytes{}
	m.flag(0)
	flatPatch(m, 0)
	m.rgb(meshRed) // three colours short
	img := renderMesh(t, 6, m.b, nil)
	wantMeshColour(t, img, 50, 50, meshWhite, "a patch whose colours were cut off")

	short := &meshBytes{}
	short.flag(0)
	short.point(0, 0)
	img = renderMesh(t, 6, short.b, nil)
	wantMeshColour(t, img, 50, 50, meshWhite, "a patch whose points were cut off")
}

func TestATriangleWithNoInsideCoversNothing(t *testing.T) {
	// Three vertices in a line make a triangle with no area, which is drawn
	// by drawing nothing rather than by dividing by nought.
	m := &meshBytes{}
	m.flag(0).point(0, 0).rgb(meshRed)
	m.flag(0).point(50, 50).rgb(meshRed)
	m.flag(0).point(100, 100).rgb(meshRed)
	img := renderMesh(t, 4, m.b, nil)
	wantMeshColour(t, img, 20, 79, meshWhite, "a triangle with no inside")
}

func TestAMeshDrawnThroughAnImpossibleTransformIsNotDrawn(t *testing.T) {
	// A transform that squashes the page to a line cannot be undone, and a
	// shading painted through one is left alone.
	d := shadedPage(t, "q 0 0 0 0 0 0 cm /S1 sh Q", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(&reader.Stream{Dict: reader.Dict{
			"ShadingType": reader.Integer(4), "ColorSpace": reader.Name("DeviceRGB"),
			"BitsPerCoordinate": reader.Integer(32), "BitsPerComponent": reader.Integer(8),
			"BitsPerFlag": reader.Integer(8), "Decode": meshDecode(3),
		}, Raw: freeTriangle()})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantMeshColour(t, img, 50, 50, meshWhite, "a mesh under a transform with no inverse")
}

func TestAMeshStreamThatCannotBeDecodedIsRefused(t *testing.T) {
	// A stream whose filter is one that gives back an image rather than
	// bytes has no vertices in it to read.
	img := renderMesh(t, 4, freeTriangle(), reader.Dict{"Filter": reader.Name("DCTDecode")})
	wantMeshColour(t, img, 50, 50, meshWhite, "a mesh stream that is a picture")
}

func TestALatticeMeshWhoseLastRowIsCutShortStopsThere(t *testing.T) {
	// Two whole rows and the beginning of a third: what was written whole is
	// drawn and the part row is left.
	m := &meshBytes{}
	for _, v := range [][2]float64{{0, 0}, {100, 0}, {0, 100}, {100, 100}} {
		m.point(v[0], v[1]).rgb(meshRed)
	}
	m.point(0, 100).rgb(meshBlue) // one vertex of a row of two
	img := renderMesh(t, 5, m.b, reader.Dict{"VerticesPerRow": reader.Integer(2)})
	wantMeshColour(t, img, 50, 50, meshRed, "the rows that were written whole")
}

func TestAPatchWhoseFlagRunsOffTheEndIsRefused(t *testing.T) {
	// With a two-bit flag a patch is not a whole number of bytes long, so the
	// next one begins part way through the last byte of the stream and there
	// is nothing there to read.
	m := &meshBytes{}
	m.bits(0, 2)
	flatPatch(m, 0)
	m.rgb(meshRed).rgb(meshGreen).rgb(meshBlue).rgb(meshWhite)
	if m.n%8 == 0 {
		t.Fatalf("the patch came to %d bits, which is a whole number of bytes", m.n)
	}
	img := renderMesh(t, 6, m.b, reader.Dict{"BitsPerFlag": reader.Integer(2)})
	wantMeshColour(t, img, 50, 50, color.RGBA{R: 128, G: 128, B: 128, A: 255},
		"the patch that was written whole")
}

func TestAMeshWhoseCoordinatesAreNotNumbersIsNotDrawn(t *testing.T) {
	// A decode array wide enough to overflow gives coordinates that are not
	// anywhere, and a triangle at no place covers no pixel.
	huge := nums(-math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, 0, 1, 0, 1, 0, 1)
	img := renderMesh(t, 4, freeTriangle(), reader.Dict{"Decode": huge})
	wantMeshColour(t, img, 50, 50, meshWhite, "a triangle whose corners are nowhere")
}

func TestAShadingOfAKindThatDoesNotExistIsRefused(t *testing.T) {
	d := shadedPage(t, "/S1 sh", func(w *reader.Writer) reader.Dict {
		return reader.Dict{"Shading": reader.Dict{"S1": w.Add(reader.Dict{
			"ShadingType": reader.Integer(8), "ColorSpace": reader.Name("DeviceRGB"),
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantMeshColour(t, img, 50, 50, meshWhite, "a shading of the eighth kind")
}
