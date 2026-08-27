package render

import (
	"github.com/go-gfx/gfx/geometry"
	"image/color"
	"testing"

	"github.com/go-pdfkit/reader"
)

// annotatedPage builds a page with whatever annotations the test wants beside
// its content.
func annotatedPage(t *testing.T, content string, build func(w *reader.Writer) reader.Array) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	annots := build(w)
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(100)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
	}
	if len(annots) > 0 {
		page["Annots"] = annots
	}
	pageRef := w.Add(page)
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
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

// blackBox is an appearance that fills the whole of its own bounding box.
func blackBox(w *reader.Writer, box reader.Array, extra reader.Dict) reader.Object {
	dict := reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"BBox": box}
	for k, v := range extra {
		dict[k] = v
	}
	return w.Add(&reader.Stream{Dict: dict,
		Raw: []byte("0 g 0 0 20 20 re f")})
}

func TestAnAnnotationIsDrawnWhereThePagePutIt(t *testing.T) {
	// The page says where; the appearance says what. A filled-in form is a
	// page whose content is blank and whose annotations are everything.
	d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
		return reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
			"Rect": nums(10, 60, 50, 90),
			"AP":   reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20), nil)},
		})}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 30, 25, color.RGBA{A: 255}, 4)
	wantWhite(t, img, 70, 25)
	wantWhite(t, img, 30, 75)
}

func TestAPageMayBeDrawnWithoutItsAnnotations(t *testing.T) {
	d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
		return reader.Array{w.Add(reader.Dict{
			"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
			"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20), nil)},
		})}
	})
	img, err := Page(d, 1, Options{Scale: 1, NoAnnotations: true})
	if err != nil {
		t.Fatal(err)
	}
	wantWhite(t, img, 30, 25)
}

func TestAnAppearanceIsStretchedOntoItsRectangle(t *testing.T) {
	// The appearance is drawn in a space of its own; the rectangle says where
	// the result goes, whatever size it was drawn at.
	d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
		return reader.Array{w.Add(reader.Dict{
			"Subtype": reader.Name("Widget"), "Rect": nums(0, 0, 100, 100),
			"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20), nil)},
		})}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range [][2]int{{5, 5}, {95, 5}, {5, 95}, {95, 95}, {50, 50}} {
		wantColour(t, img, at[0], at[1], color.RGBA{A: 255}, 4)
	}
}

func TestAnAppearanceDrawnAtAnAngleLandsInsideItsBox(t *testing.T) {
	// A stamp put down turned is drawn turned, and what the page gets is the
	// smallest rectangle round the result stretched onto the one it named —
	// which is what keeps it from running off the page.
	d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
		return reader.Array{w.Add(reader.Dict{
			"Subtype": reader.Name("Widget"), "Rect": nums(20, 20, 80, 80),
			"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20),
				reader.Dict{"Matrix": nums(0.7071, 0.7071, -0.7071, 0.7071, 0, 0)})},
		})}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 50, 50, color.RGBA{A: 255}, 4)
	// Outside the rectangle the page named, nothing.
	for _, at := range [][2]int{{5, 50}, {95, 50}, {50, 5}, {50, 95}} {
		wantWhite(t, img, at[0], at[1])
	}
}

func TestAWidgetShowsTheStateItIsIn(t *testing.T) {
	// A checkbox carries a picture for each of its states and says which one
	// it is in. That is how a tick shows.
	for _, c := range []struct {
		why    string
		as     reader.Object
		wantOn bool
	}{
		{"ticked", reader.Name("Yes"), true},
		{"not ticked", reader.Name("Off"), false},
		{"in a state it has no picture for", reader.Name("Maybe"), false},
	} {
		d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
			dict := reader.Dict{
				"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": reader.Dict{
					"Yes": blackBox(w, nums(0, 0, 20, 20), nil),
					"Off": w.Add(&reader.Stream{Dict: reader.Dict{
						"Subtype": reader.Name("Form"), "BBox": nums(0, 0, 20, 20)},
						Raw: []byte("")}),
				}},
			}
			if c.as != nil {
				dict["AS"] = c.as
			}
			return reader.Array{w.Add(dict)}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got := isWhite(img, 30, 25); got == c.wantOn {
			t.Errorf("%s: the box is %s", c.why, map[bool]string{true: "blank", false: "filled"}[got])
		}
	}
}

func TestAnAppearanceSetOfOneNeedsNoStateNamed(t *testing.T) {
	// One picture and no state named is unambiguous; two and no state named
	// is the file not saying which it means.
	for _, c := range []struct {
		why   string
		count int
		want  bool
	}{{"one picture", 1, true}, {"two pictures", 2, false}} {
		d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
			states := reader.Dict{"Yes": blackBox(w, nums(0, 0, 20, 20), nil)}
			if c.count > 1 {
				states["No"] = blackBox(w, nums(0, 0, 20, 20), nil)
			}
			return reader.Array{w.Add(reader.Dict{
				"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": states},
			})}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		if drawn := !isWhite(img, 30, 25); drawn != c.want {
			t.Errorf("%s: drawn %v, wanted %v", c.why, drawn, c.want)
		}
	}
}

func TestAnAnnotationThePageDoesNotShow(t *testing.T) {
	for _, c := range []struct {
		why   string
		extra reader.Dict
	}{
		{"one marked hidden", reader.Dict{"F": reader.Integer(annotHidden)}},
		{"one marked not for a screen", reader.Dict{"F": reader.Integer(annotNoView)}},
		{"a popup, which is a window and not part of the page",
			reader.Dict{"Subtype": reader.Name("Popup")}},
	} {
		d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
			dict := reader.Dict{
				"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20), nil)},
			}
			for k, v := range c.extra {
				dict[k] = v
			}
			return reader.Array{w.Add(dict)}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		wantWhite(t, img, 30, 25)
	}
}

func TestAnAnnotationWithNothingToDraw(t *testing.T) {
	// A link is a place to click, not a thing to draw, and 33 334 of the
	// pdf.js corpus's 33 783 annotations without an appearance are links.
	for _, c := range []struct {
		why  string
		make func(w *reader.Writer) reader.Dict
	}{
		{"no appearance at all", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Link"), "Rect": nums(10, 60, 50, 90)}
		}},
		{"an appearance dictionary with no normal one", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"D": blackBox(w, nums(0, 0, 20, 20), nil)}}
		}},
		{"a normal appearance that is neither a stream nor a set",
			func(w *reader.Writer) reader.Dict {
				return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
					"AP": reader.Dict{"N": reader.Integer(3)}}
			}},
		{"a state naming something that is not a stream", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AS": reader.Name("Yes"),
				"AP": reader.Dict{"N": reader.Dict{"Yes": reader.Integer(1)}}}
		}},
		{"a set of one that is not a stream", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": reader.Dict{"Yes": reader.Integer(1)}}}
		}},
		{"no rectangle to draw it in", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"),
				"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20), nil)}}
		}},
		{"an appearance with no bounding box", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": w.Add(&reader.Stream{
					Dict: reader.Dict{"Subtype": reader.Name("Form")}, Raw: []byte("0 g 0 0 20 20 re f")})}}
		}},
		{"an appearance that is a picture rather than a drawing", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": w.Add(&reader.Stream{
					Dict: reader.Dict{"Subtype": reader.Name("Form"), "BBox": nums(0, 0, 20, 20),
						"Filter": reader.Name("DCTDecode")}, Raw: []byte("not a jpeg")})}}
		}},
		{"a box whose corners come out past any number", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 1e308, 1e308), reader.Dict{
					"Matrix": nums(1e308, 0, 0, 1e308, 0, 0)})}}
		}},
		{"a matrix that is not numbers", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Widget"), "Rect": nums(10, 60, 50, 90),
				"AP": reader.Dict{"N": blackBox(w, nums(0, 0, 20, 20), reader.Dict{
					"Matrix": reader.Array{reader.Name("x"), reader.Integer(0), reader.Integer(0),
						reader.Integer(1), reader.Integer(0), reader.Integer(0)}})}}
		}},
	} {
		d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
			return reader.Array{w.Add(c.make(w))}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatalf("%s: %v", c.why, err)
		}
		if !isWhite(img, 30, 25) {
			t.Errorf("%s: something was drawn", c.why)
		}
	}
}

func TestAnAnnotationListHoldingSomethingElse(t *testing.T) {
	d := annotatedPage(t, "", func(w *reader.Writer) reader.Array {
		return reader.Array{reader.Integer(4)}
	})
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestAPageWhoseAnnotationsAreNotAList(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": nums(0, 0, 100, 100), "Annots": reader.Integer(9),
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestFittingABoxThatHasNoSize(t *testing.T) {
	// A bounding box squashed to nothing cannot be stretched onto anything,
	// and one whose numbers are not numbers is a file being wrong.
	if _, ok := fitBox(geometry.Matrix{}, [4]float64{0, 0, 0, 0}, [4]float64{0, 0, 10, 10}); !ok {
		t.Error("a box of no size should still place, at its corner")
	}
	huge := geometry.Matrix{Xx: 1e308, Yy: 1e308}
	if _, ok := fitBox(huge, [4]float64{0, 0, 1e308, 1e308}, [4]float64{0, 0, 10, 10}); ok {
		t.Error("a box whose corners are not numbers was placed")
	}
}
