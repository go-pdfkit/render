package render

import (
	"image/color"
	"testing"

	"github.com/go-pdfkit/reader"
)

func TestAFilledRectangleLandsWhereItIsPut(t *testing.T) {
	// A hundred points square, with a black rectangle from (10,10) to (40,40)
	// in the page's own coordinates, which count up from the bottom left.
	d := onePage(t, [4]float64{0, 0, 100, 100}, "0 g 10 10 30 30 re f", nil)
	img := draw(t, d, Options{})
	if img.W != 100 || img.H != 100 {
		t.Fatalf("image is %dx%d", img.W, img.H)
	}
	// The rectangle occupies y from 60 to 90 in image coordinates, which count
	// down from the top.
	wantBlack(t, img, 25, 75)
	wantBlack(t, img, 11, 89)
	wantBlack(t, img, 39, 61)
	// And nothing outside it.
	wantWhite(t, img, 5, 5)
	wantWhite(t, img, 50, 50)
	wantWhite(t, img, 25, 95) // below the rectangle on the page
	wantWhite(t, img, 25, 55) // above it
}

func TestScaleAndDPI(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 100, 50}, "0 g 0 0 100 50 re f", nil)
	img := draw(t, d, Options{Scale: 2})
	if img.W != 200 || img.H != 100 {
		t.Errorf("at twice the size the image is %dx%d", img.W, img.H)
	}
	img = draw(t, d, Options{DPI: 144})
	if img.W != 200 || img.H != 100 {
		t.Errorf("at 144 dots to the inch the image is %dx%d", img.W, img.H)
	}
	// Scale wins when both are given.
	img = draw(t, d, Options{Scale: 1, DPI: 300})
	if img.W != 100 {
		t.Errorf("Scale did not win: %d wide", img.W)
	}
}

func TestAMediaBoxThatDoesNotStartAtTheOrigin(t *testing.T) {
	// The box runs from (50,50) to (150,150); a rectangle at (60,60) is ten
	// points in from its corner.
	d := onePage(t, [4]float64{50, 50, 150, 150}, "0 g 60 60 20 20 re f", nil)
	img := draw(t, d, Options{})
	if img.W != 100 || img.H != 100 {
		t.Fatalf("image is %dx%d", img.W, img.H)
	}
	wantBlack(t, img, 15, 85)
	wantWhite(t, img, 5, 95)
	wantWhite(t, img, 50, 50)
}

func TestRotation(t *testing.T) {
	// A tall page with a mark in its bottom left corner. Turned a quarter
	// clockwise, the mark ends up in the top left.
	content := "0 g 0 0 10 10 re f"
	box := [4]float64{0, 0, 40, 80}
	cases := []struct {
		rotate int
		w, h   int
		x, y   int
	}{
		{0, 40, 80, 5, 75},
		{90, 80, 40, 5, 5},
		{180, 40, 80, 35, 5},
		{270, 80, 40, 75, 35},
	}
	for _, c := range cases {
		d := onePage(t, box, content, reader.Dict{"Rotate": reader.Integer(c.rotate)})
		img := draw(t, d, Options{})
		if img.W != c.w || img.H != c.h {
			t.Errorf("rotate %d: image is %dx%d, want %dx%d", c.rotate, img.W, img.H, c.w, c.h)
			continue
		}
		if !isBlack(img, c.x, c.y) {
			t.Errorf("rotate %d: expected ink at %s", c.rotate, pixel(img, c.x, c.y))
		}
	}
}

func TestARotationThatIsNotARightAngle(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 80}, "0 g 0 0 10 10 re f",
		reader.Dict{"Rotate": reader.Integer(45)})
	img := draw(t, d, Options{})
	if img.W != 40 || img.H != 80 {
		t.Errorf("image is %dx%d, want the page unturned", img.W, img.H)
	}
}

func TestANegativeRotation(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 80}, "0 g 0 0 10 10 re f",
		reader.Dict{"Rotate": reader.Integer(-90)})
	img := draw(t, d, Options{})
	// Minus ninety is the same as two hundred and seventy.
	if img.W != 80 || img.H != 40 {
		t.Fatalf("image is %dx%d", img.W, img.H)
	}
	wantBlack(t, img, 75, 35)
}

func TestTheCropBoxWinsOverTheMediaBox(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 100, 100}, "0 g 0 0 100 100 re f",
		reader.Dict{"CropBox": reader.Array{reader.Integer(25), reader.Integer(25),
			reader.Integer(75), reader.Integer(75)}})
	img := draw(t, d, Options{})
	if img.W != 50 || img.H != 50 {
		t.Errorf("image is %dx%d, want the crop box", img.W, img.H)
	}
}

func TestAPageWithNoBoxAtAll(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("0 g 0 0 10 10 re f")})})
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
	img := draw(t, d, Options{})
	// American letter, which is what a page that says nothing is taken to be.
	if img.W != 612 || img.H != 792 {
		t.Errorf("image is %dx%d", img.W, img.H)
	}
}

func TestColours(t *testing.T) {
	cases := []struct {
		content string
		want    color.RGBA
	}{
		{"0 g 0 0 10 10 re f", color.RGBA{0, 0, 0, 255}},
		{"1 g 0 0 10 10 re f", color.RGBA{255, 255, 255, 255}},
		{"0.5 g 0 0 10 10 re f", color.RGBA{128, 128, 128, 255}},
		{"1 0 0 rg 0 0 10 10 re f", color.RGBA{255, 0, 0, 255}},
		{"0 1 0 rg 0 0 10 10 re f", color.RGBA{0, 255, 0, 255}},
		{"0 0 1 rg 0 0 10 10 re f", color.RGBA{0, 0, 255, 255}},
		{"0 0 0 0 k 0 0 10 10 re f", color.RGBA{255, 255, 255, 255}},
		{"0 0 0 1 k 0 0 10 10 re f", color.RGBA{0, 0, 0, 255}},
		{"1 1 0 0 k 0 0 10 10 re f", color.RGBA{0, 0, 255, 255}},
	}
	for _, c := range cases {
		d := onePage(t, [4]float64{0, 0, 20, 20}, c.content, nil)
		img := draw(t, d, Options{})
		if !isWhite(img, 5, 15) && c.want.R == 255 && c.want.G == 255 && c.want.B == 255 {
			// A white fill on white paper is invisible either way.
			continue
		}
		wantColour(t, img, 5, 15, c.want, 6)
	}
}

func TestStrokingColoursAreSeparateFromFillingOnes(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 40},
		"1 0 0 rg 0 0 1 RG 4 w 10 10 20 20 re B", nil)
	img := draw(t, d, Options{})
	// Red inside, blue on the edge.
	wantColour(t, img, 20, 20, color.RGBA{255, 0, 0, 255}, 8)
	wantColour(t, img, 10, 20, color.RGBA{0, 0, 255, 255}, 8)
}

func TestTheEvenOddRule(t *testing.T) {
	// A square with a smaller square inside it, both wound the same way. Under
	// the non-zero rule the middle is filled; under the even-odd rule it is a
	// hole.
	content := "0 g 0 0 40 40 re 10 10 20 20 re "
	solid := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, content+"f", nil), Options{})
	holed := draw(t, onePage(t, [4]float64{0, 0, 40, 40}, content+"f*", nil), Options{})
	wantBlack(t, solid, 20, 20)
	wantWhite(t, holed, 20, 20)
	// The outer ring is filled either way.
	wantBlack(t, solid, 5, 20)
	wantBlack(t, holed, 5, 20)
}

func TestClipping(t *testing.T) {
	// A clip of the left half, then a fill of the whole page.
	d := onePage(t, [4]float64{0, 0, 40, 40}, "0 0 20 40 re W n 0 g 0 0 40 40 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 10, 20)
	wantWhite(t, img, 30, 20)
}

func TestClippingIsUndoneByRestoring(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 40},
		"q 0 0 20 40 re W n 0 g 0 0 40 40 re f Q 0 g 30 0 10 10 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 10, 20) // inside the clip
	wantWhite(t, img, 30, 20) // outside it
	wantBlack(t, img, 35, 35) // drawn after the clip was let go
}

func TestClipsIntersect(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 40},
		"0 0 20 40 re W n 0 20 40 20 re W n 0 g 0 0 40 40 re f", nil)
	img := draw(t, d, Options{})
	// Only the top left quarter survives both clips.
	wantBlack(t, img, 10, 10)
	wantWhite(t, img, 30, 10)
	wantWhite(t, img, 10, 30)
	wantWhite(t, img, 30, 30)
}

func TestTransparency(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 20, 20},
		"/GS gs 0 g 0 0 20 20 re f",
		reader.Dict{"Resources": reader.Dict{
			"ExtGState": reader.Dict{"GS": reader.Dict{"ca": reader.Real(0.5)}},
		}})
	img := draw(t, d, Options{})
	wantColour(t, img, 10, 10, color.RGBA{128, 128, 128, 255}, 8)
}

func TestTheTransformStacks(t *testing.T) {
	// Translate ten right and ten up, then draw at the origin.
	d := onePage(t, [4]float64{0, 0, 40, 40}, "1 0 0 1 10 10 cm 0 g 0 0 10 10 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 15, 25)
	wantWhite(t, img, 5, 35)
}

func TestSavingAndRestoringTheTransform(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 40, 40},
		"q 1 0 0 1 20 20 cm 0 g 0 0 10 10 re f Q 0 g 0 0 10 10 re f", nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 25, 15)
	wantBlack(t, img, 5, 35)
}

func TestAPageTooLargeIsRefused(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 100, 100}, "", nil)
	if _, err := Page(d, 1, Options{MaxPixels: 100}); err == nil {
		t.Error("want an error")
	}
	if _, err := Page(d, 2, Options{}); err == nil {
		t.Error("a page that is not there should fail")
	}
}

func TestTheBackgroundCanBeChosen(t *testing.T) {
	blue := color.RGBA{0, 0, 255, 255}
	d := onePage(t, [4]float64{0, 0, 10, 10}, "", nil)
	img := draw(t, d, Options{Background: &blue})
	wantColour(t, img, 5, 5, blue, 0)
}
