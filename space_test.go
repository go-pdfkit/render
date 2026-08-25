package render

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/go-pdfkit/reader"
)

// withResources builds a one-page document carrying the given resources.
func withResources(t *testing.T, content string, resources reader.Dict) *reader.Document {
	t.Helper()
	return onePage(t, [4]float64{0, 0, 20, 20}, content, reader.Dict{"Resources": resources})
}

func TestNamedColourSpaces(t *testing.T) {
	cases := []struct {
		name  string
		space reader.Object
		set   string
		want  color.RGBA
	}{
		{"a profile of one component", reader.Array{reader.Name("ICCBased"), nil}, "0 sc", color.RGBA{0, 0, 0, 255}},
		{"calibrated grey", reader.Array{reader.Name("CalGray"), reader.Dict{}}, "1 sc", color.RGBA{255, 255, 255, 255}},
		{"calibrated colour", reader.Array{reader.Name("CalRGB"), reader.Dict{}}, "1 0 0 sc", color.RGBA{255, 0, 0, 255}},
		{"the device spaces by array", reader.Array{reader.Name("DeviceCMYK")}, "0 0 0 1 sc", color.RGBA{0, 0, 0, 255}},
		{"lightness and two axes", reader.Array{reader.Name("Lab"), reader.Dict{}}, "50 20 -30 sc", color.RGBA{128, 128, 128, 255}},
		{"a spot colour at full strength", reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray"), reader.Dict{}}, "1 sc", color.RGBA{0, 0, 0, 255}},
		{"a spot colour at none", reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray"), reader.Dict{}}, "0 sc", color.RGBA{255, 255, 255, 255}},
		{"several inks at once", reader.Array{reader.Name("DeviceN"),
			reader.Array{reader.Name("A"), reader.Name("B")},
			reader.Name("DeviceGray"), reader.Dict{}}, "0 1 sc", color.RGBA{0, 0, 0, 255}},
		{"a space nobody has heard of", reader.Array{reader.Name("Nonesuch")}, "0 sc", color.RGBA{0, 0, 0, 255}},
		{"a space that is not an array", reader.Integer(7), "0 sc", color.RGBA{0, 0, 0, 255}},
		{"an empty array", reader.Array{}, "0 sc", color.RGBA{0, 0, 0, 255}},
	}
	for _, c := range cases {
		d := withResources(t, "/S cs "+c.set+" 0 0 20 20 re f",
			reader.Dict{"ColorSpace": reader.Dict{"S": c.space}})
		img := draw(t, d, Options{})
		if c.want.R == 255 && c.want.G == 255 && c.want.B == 255 {
			if !isWhite(img, 10, 10) {
				t.Errorf("%s: %s", c.name, pixel(img, 10, 10))
			}
			continue
		}
		wantColour(t, img, 10, 10, c.want, 12)
	}
}

func TestAProfileSaysHowManyComponentsItHas(t *testing.T) {
	for _, c := range []struct {
		n    int
		set  string
		want color.RGBA
	}{
		{1, "0 sc", color.RGBA{0, 0, 0, 255}},
		{3, "1 0 0 sc", color.RGBA{255, 0, 0, 255}},
		{4, "1 1 0 0 sc", color.RGBA{0, 0, 255, 255}},
	} {
		w := reader.NewWriter("1.7")
		profile := w.Add(&reader.Stream{Dict: reader.Dict{"N": reader.Integer(c.n)}, Raw: []byte("x")})
		pagesRef := w.Reserve()
		page := w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
				Raw: []byte("/S cs " + c.set + " 0 0 20 20 re f")}),
			"Resources": reader.Dict{"ColorSpace": reader.Dict{
				"S": reader.Array{reader.Name("ICCBased"), profile}}},
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
		wantColour(t, draw(t, d, Options{}), 10, 10, c.want, 12)
	}
}

func TestIndexedColourSpaces(t *testing.T) {
	// A table of three colours: red, green, blue.
	table := reader.String([]byte{255, 0, 0, 0, 255, 0, 0, 0, 255})
	space := reader.Array{reader.Name("Indexed"), reader.Name("DeviceRGB"), reader.Integer(2), table}
	for i, want := range []color.RGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255},
	} {
		d := withResources(t, fmt.Sprintf("/S cs %d sc 0 0 20 20 re f", i),
			reader.Dict{"ColorSpace": reader.Dict{"S": space}})
		wantColour(t, draw(t, d, Options{}), 10, 10, want, 4)
	}
	// An index past the end of the table draws black rather than nothing.
	d := withResources(t, "/S cs 9 sc 0 0 20 20 re f",
		reader.Dict{"ColorSpace": reader.Dict{"S": space}})
	wantBlack(t, draw(t, d, Options{}), 10, 10)
	// And one below it reads as the first entry.
	d = withResources(t, "/S cs -3 sc 0 0 20 20 re f",
		reader.Dict{"ColorSpace": reader.Dict{"S": space}})
	wantColour(t, draw(t, d, Options{}), 10, 10, color.RGBA{255, 0, 0, 255}, 4)
}

func TestAnIndexedTableInAStream(t *testing.T) {
	w := reader.NewWriter("1.7")
	table := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{0, 0, 255}})
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("/S cs 0 sc 0 0 20 20 re f")}),
		"Resources": reader.Dict{"ColorSpace": reader.Dict{"S": reader.Array{
			reader.Name("Indexed"), reader.Name("DeviceRGB"), reader.Integer(0), table}}},
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
	wantColour(t, draw(t, d, Options{}), 10, 10, color.RGBA{0, 0, 255, 255}, 4)
}

func TestIndexedSpacesInShapesNobodyShouldWrite(t *testing.T) {
	for _, space := range []reader.Object{
		reader.Array{reader.Name("Indexed")},
		reader.Array{reader.Name("Indexed"), reader.Name("DeviceRGB"), reader.Integer(1), reader.Integer(7)},
	} {
		d := withResources(t, "/S cs 0 sc 0 0 20 20 re f",
			reader.Dict{"ColorSpace": reader.Dict{"S": space}})
		if img := draw(t, d, Options{}); img == nil {
			t.Error("nothing came back")
		}
	}
}

func TestAPatternIsDrawnInGreyForNow(t *testing.T) {
	d := withResources(t, "/Pattern cs /P scn 0 0 20 20 re f", reader.Dict{})
	img := draw(t, d, Options{})
	wantColour(t, img, 10, 10, color.RGBA{128, 128, 128, 255}, 8)
}

func TestASpaceThatIsNotInTheResources(t *testing.T) {
	// Nothing named it, so the drawing falls back on grey and the mark stays.
	d := withResources(t, "/Missing cs 0 sc 0 0 20 20 re f", reader.Dict{})
	wantBlack(t, draw(t, d, Options{}), 10, 10)
	d = withResources(t, "/Missing cs 0 sc 0 0 20 20 re f",
		reader.Dict{"ColorSpace": reader.Dict{"Other": reader.Name("DeviceRGB")}})
	wantBlack(t, draw(t, d, Options{}), 10, 10)
}

func TestSettingASpaceResetsTheColour(t *testing.T) {
	// Red, then a new space, then a fill: the fill is that space's black, not
	// the red that came before it.
	d := onePage(t, [4]float64{0, 0, 20, 20}, "1 0 0 rg /DeviceRGB cs 0 0 20 20 re f", nil)
	wantBlack(t, draw(t, d, Options{}), 10, 10)
	d = onePage(t, [4]float64{0, 0, 20, 20}, "1 0 0 RG /DeviceRGB CS 4 w 0 0 m 20 20 l S", nil)
	wantBlack(t, draw(t, d, Options{}), 10, 10)
	// A cs with no operand changes nothing.
	d = onePage(t, [4]float64{0, 0, 20, 20}, "1 0 0 rg cs 0 0 20 20 re f", nil)
	wantColour(t, draw(t, d, Options{}), 10, 10, color.RGBA{255, 0, 0, 255}, 4)
}

func TestCMYKStartsAtBlack(t *testing.T) {
	// The one space whose black is not all zeroes.
	if got := deviceCMYK.initial(); got.R > 20 || got.G > 20 || got.B > 20 {
		t.Errorf("CMYK starts at %d,%d,%d", got.R, got.G, got.B)
	}
	if got := deviceRGB.initial(); got.R != 0 || got.G != 0 || got.B != 0 {
		t.Errorf("RGB starts at %d,%d,%d", got.R, got.G, got.B)
	}
}

func TestOperandsThatAreNotNumbers(t *testing.T) {
	// A colour operator whose operands are not numbers reads what it can and
	// takes nothing for the rest, which is black.
	d := onePage(t, [4]float64{0, 0, 20, 20}, "/x /y rg 0 0 20 20 re f", nil)
	wantBlack(t, draw(t, d, Options{}), 10, 10)
}
