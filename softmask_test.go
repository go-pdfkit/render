package render

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/go-pdfkit/reader"
)

// maskForm builds the little drawing a soft mask reads itself off.
func maskForm(w *reader.Writer, content string, resources reader.Dict, extra reader.Dict) reader.Object {
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"BBox":  nums(0, 0, 100, 100),
		"Group": reader.Dict{"S": reader.Name("Transparency"), "CS": reader.Name("DeviceGray")},
	}
	if resources != nil {
		dict["Resources"] = resources
	}
	for k, v := range extra {
		dict[k] = v
	}
	return w.Add(&reader.Stream{Dict: dict, Raw: []byte(content)})
}

// greyRamp is a shading from black to white across the page, which makes a
// mask whose strength can be read off wherever it is looked at.
func greyRamp(w *reader.Writer) reader.Object {
	return w.Add(reader.Dict{
		"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceGray"),
		"Coords": nums(0, 0, 100, 0),
		"Function": w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1),
			"C0": nums(0), "C1": nums(1), "N": reader.Integer(1)}),
		"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
	})
}

// maskedPage paints a black rectangle over the whole page through whatever
// soft mask the state names.
func maskedPage(t *testing.T, mask func(w *reader.Writer) reader.Object, content string) *reader.Document {
	t.Helper()
	if content == "" {
		content = "/GS0 gs 0 0 100 100 re f"
	}
	return shadedPage(t, content, func(w *reader.Writer) reader.Dict {
		return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
			"Type": reader.Name("ExtGState"), "SMask": mask(w),
		})}}
	})
}

func TestALuminosityMaskFadesWhatIsDrawnThroughIt(t *testing.T) {
	// The mask is a drawing that goes from black to white across the page.
	// Where it is black nothing shows, where it is white everything does, and
	// in between a fill of black comes out grey.
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "/S1 sh", reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}, nil)
		return reader.Dict{"S": reader.Name("Luminosity"), "G": form}
	}, "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 2, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 10)
	wantColour(t, img, 50, 50, color.RGBA{R: 128, G: 128, B: 128, A: 255}, 12)
	wantColour(t, img, 97, 50, color.RGBA{A: 255}, 10)
}

func TestAMaskDoesNotReachOutsideItsOwnBox(t *testing.T) {
	// The mask's form covers the left half of the page, so the right half is
	// the backdrop — black, and therefore hidden — however the drawing inside
	// the box came out.
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "1 g 0 0 50 100 re f", nil, reader.Dict{"BBox": nums(0, 0, 50, 100)})
		return reader.Dict{"S": reader.Name("Luminosity"), "G": form}
	}, "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 25, 50, color.RGBA{A: 255}, 6)
	wantColour(t, img, 75, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
}

func TestABackdropColourSaysWhatIsOutsideTheMask(t *testing.T) {
	// A white backdrop lets everything the mask does not cover through, which
	// is the opposite of the default and what a file says when it wants a
	// mask to hide something rather than to reveal it.
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "0 g 0 0 50 100 re f", nil, reader.Dict{"BBox": nums(0, 0, 50, 100)})
		return reader.Dict{"S": reader.Name("Luminosity"), "G": form, "BC": nums(1)}
	}, "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 25, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
	wantColour(t, img, 75, 50, color.RGBA{A: 255}, 6)
}

func TestABackdropThatDoesNotFitItsSpaceIsBlack(t *testing.T) {
	for _, c := range []struct {
		why   string
		group reader.Object
		bc    reader.Array
	}{
		{"a backdrop with too few numbers for the space",
			reader.Dict{"S": reader.Name("Transparency"), "CS": reader.Name("DeviceRGB")}, nums(1)},
		{"a group that names no space at all",
			reader.Dict{"S": reader.Name("Transparency")}, nums(1)},
		{"a form with no group dictionary", nil, nums(1)},
	} {
		d := maskedPage(t, func(w *reader.Writer) reader.Object {
			extra := reader.Dict{"BBox": nums(0, 0, 50, 100)}
			if c.group == nil {
				extra["Group"] = reader.Null{}
			} else {
				extra["Group"] = c.group
			}
			form := maskForm(w, "1 g 0 0 50 100 re f", nil, extra)
			return reader.Dict{"S": reader.Name("Luminosity"), "G": form, "BC": c.bc}
		}, "")
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		wantColour(t, img, 75, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
	}
}

func TestAnAlphaMaskAsksWhetherAnythingWasDrawnAtAll(t *testing.T) {
	// This kind of mask does not care what colour came out, only how much of
	// each pixel was covered. A white rectangle on white paper is invisible
	// and still masks nothing away.
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "1 g 0 0 50 100 re f", nil, nil)
		return reader.Dict{"S": reader.Name("Alpha"), "G": form}
	}, "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 25, 50, color.RGBA{A: 255}, 6)
	wantColour(t, img, 75, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
}

func TestAnAlphaMaskCountsEveryKindOfMark(t *testing.T) {
	// Images and gradients cover a pixel as surely as a fill does, and a mask
	// of this kind has to count them too.
	for _, c := range []struct {
		why       string
		content   string
		resources reader.Dict
	}{
		{"a gradient painted on its own", "/S1 sh", nil},
		{"an image", "q 50 0 0 100 0 0 cm /I1 Do Q", nil},
	} {
		d := shadedPage(t, "/GS0 gs 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
			inner := reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)},
				"XObject": reader.Dict{"I1": w.Add(&reader.Stream{Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
					"Width": reader.Integer(1), "Height": reader.Integer(1),
					"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
				}, Raw: []byte{255}})}}
			form := maskForm(w, c.content, inner, reader.Dict{"BBox": nums(0, 0, 50, 100)})
			return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
				"SMask": reader.Dict{"S": reader.Name("Alpha"), "G": form},
			})}}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		if img.At(25, 50) == (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
			t.Errorf("%s: was not counted as having covered anything", c.why)
		}
		wantColour(t, img, 75, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
	}
}

func TestAMaskIsPutAsideAgain(t *testing.T) {
	// A state that names /None takes the mask off, and one that says nothing
	// about masks at all leaves whatever is in force alone.
	d := shadedPage(t, "/GS0 gs /GS1 gs 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
		form := maskForm(w, "0 g 0 0 100 100 re f", nil, nil)
		return reader.Dict{"ExtGState": reader.Dict{
			"GS0": w.Add(reader.Dict{"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form}}),
			"GS1": w.Add(reader.Dict{"SMask": reader.Name("None")}),
		}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 50, 50, color.RGBA{A: 255}, 6)

	d = shadedPage(t, "/GS0 gs /GS1 gs 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
		form := maskForm(w, "0 g 0 0 100 100 re f", nil, nil)
		return reader.Dict{"ExtGState": reader.Dict{
			"GS0": w.Add(reader.Dict{"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form}}),
			"GS1": w.Add(reader.Dict{"LW": reader.Integer(4)}),
		}}
	})
	img, err = Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 50, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
}

func TestAMaskIsDrawnOnceHoweverOftenItIsUsed(t *testing.T) {
	// The same graphics state named twice is the same mask twice, and drawing
	// it again would be drawing the same picture again.
	d := shadedPage(t, "/GS0 gs 0 0 50 100 re f /GS0 gs 50 0 50 100 re f",
		func(w *reader.Writer) reader.Dict {
			form := maskForm(w, "/S1 sh", reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}, nil)
			return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
				"SMask": w.Add(reader.Dict{"S": reader.Name("Luminosity"), "G": form}),
			})}}
		})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 2, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 10)
	wantColour(t, img, 97, 50, color.RGBA{A: 255}, 10)
}

func TestATransferFunctionTurnsTheMaskRound(t *testing.T) {
	// A transfer function is a second thought about the mask: this one gives
	// back one minus what it was handed, so the fade runs the other way.
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "/S1 sh", reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}, nil)
		return reader.Dict{"S": reader.Name("Luminosity"), "G": form,
			"TR": w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(4), "Domain": nums(0, 1), "Range": nums(0, 1),
			}, Raw: []byte("{ 1 exch sub }")}),
		}
	}, "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 2, 50, color.RGBA{A: 255}, 10)
	wantColour(t, img, 97, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 10)
}

func TestATransferFunctionThatSaysNothingIsSkipped(t *testing.T) {
	for _, tr := range []reader.Object{reader.Name("Identity"), reader.Name("Nonsense"), reader.Null{}} {
		d := maskedPage(t, func(w *reader.Writer) reader.Object {
			form := maskForm(w, "/S1 sh", reader.Dict{"Shading": reader.Dict{"S1": greyRamp(w)}}, nil)
			return reader.Dict{"S": reader.Name("Luminosity"), "G": form, "TR": tr}
		}, "")
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		wantColour(t, img, 2, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 10)
		wantColour(t, img, 97, 50, color.RGBA{A: 255}, 10)
	}
}

func TestAMaskThatCannotBeReadMasksNothing(t *testing.T) {
	// A file may say something about a mask that cannot be made sense of. The
	// page is then drawn at full strength, which is what this package did
	// before it could read masks at all, rather than blanked.
	for _, c := range []struct {
		why  string
		make func(w *reader.Writer) reader.Object
	}{
		{"a mask of a kind that does not exist", func(w *reader.Writer) reader.Object {
			return reader.Dict{"S": reader.Name("Sepia"),
				"G": maskForm(w, "0 g 0 0 100 100 re f", nil, nil)}
		}},
		{"a mask whose group is not a form", func(w *reader.Writer) reader.Object {
			return reader.Dict{"S": reader.Name("Luminosity"), "G": reader.Integer(4)}
		}},
		{"a mask that is neither a name nor a dictionary", func(w *reader.Writer) reader.Object {
			return reader.Integer(7)
		}},
		{"a mask whose form is a picture rather than a drawing", func(w *reader.Writer) reader.Object {
			return reader.Dict{"S": reader.Name("Luminosity"), "G": w.Add(&reader.Stream{
				Dict: reader.Dict{"Subtype": reader.Name("Form"), "BBox": nums(0, 0, 100, 100),
					"Filter": reader.Name("DCTDecode")}, Raw: []byte("not a jpeg")})}
		}},
		{"a mask whose form has a matrix that is not numbers", func(w *reader.Writer) reader.Object {
			return reader.Dict{"S": reader.Name("Luminosity"),
				"G": maskForm(w, "0 g 0 0 100 100 re f", nil, reader.Dict{
					"Matrix": reader.Array{reader.Name("x"), reader.Integer(0), reader.Integer(0),
						reader.Integer(1), reader.Integer(0), reader.Integer(0)}})}
		}},
	} {
		d := maskedPage(t, c.make, "")
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got := img.At(50, 50); got != (color.RGBA{A: 255}) {
			t.Errorf("%s: middle of the page is %v, wanted it drawn at full strength", c.why, got)
		}
	}
}

func TestAMaskFormMayHaveAMatrixOfItsOwn(t *testing.T) {
	// The form's matrix moves the mask, so a mask drawn on the left half of
	// its own space can end up over the right half of the page.
	d := maskedPage(t, func(w *reader.Writer) reader.Object {
		form := maskForm(w, "1 g 0 0 50 100 re f", nil, reader.Dict{
			"BBox":   nums(0, 0, 50, 100),
			"Matrix": nums(1, 0, 0, 1, 50, 0),
		})
		return reader.Dict{"S": reader.Name("Luminosity"), "G": form}
	}, "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 25, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 6)
	wantColour(t, img, 75, 50, color.RGBA{A: 255}, 6)
}

func TestATransparencyGroupGoesOnAsOneThing(t *testing.T) {
	// Two shapes that overlap, drawn at half strength inside a group, come
	// out the same shade where they overlap as where they do not: the group
	// is drawn whole and then faded, not faded mark by mark. Drawing them one
	// at a time would darken the overlap, which is what a shadow made of
	// several shapes looks like when it is got wrong.
	d := shadedPage(t, "/GS0 gs /F1 Do", func(w *reader.Writer) reader.Dict {
		form := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":  nums(0, 0, 100, 100),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 10 10 50 50 re f 30 10 50 50 re f")})
		return reader.Dict{
			"XObject":   reader.Dict{"F1": form},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"ca": nums(0.5)[0]})},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	alone, overlap := img.At(20, 60), img.At(45, 60)
	if alone != overlap {
		t.Errorf("one shape is %v and the overlap %v; a group goes on as one thing", alone, overlap)
	}
	wantColour(t, img, 20, 60, color.RGBA{R: 128, G: 128, B: 128, A: 255}, 4)
	wantColour(t, img, 90, 60, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 4)
}

func TestATransparencyGroupThatIsWhollyHiddenLeavesThePageAlone(t *testing.T) {
	d := shadedPage(t, "/GS0 gs /F1 Do", func(w *reader.Writer) reader.Dict {
		form := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":  nums(0, 0, 100, 100),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 10 10 50 50 re f")})
		return reader.Dict{
			"XObject":   reader.Dict{"F1": form},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"ca": nums(0)[0]})},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 20, 60, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 2)
}

func TestATransparencyGroupWithNoBoxOnThePageDrawsNothing(t *testing.T) {
	d := shadedPage(t, "/GS0 gs /F1 Do", func(w *reader.Writer) reader.Dict {
		form := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":  nums(500, 500, 600, 600),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 500 500 100 100 re f")})
		return reader.Dict{
			"XObject":   reader.Dict{"F1": form},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"ca": nums(0.5)[0]})},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 50, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 2)
}

func TestAGroupInsideAMaskCountsForWhatItCovered(t *testing.T) {
	// A mask that asks how much was painted, over a drawing that is itself a
	// group faded to half: half of it was painted, so half of what the page
	// draws through the mask shows.
	d := shadedPage(t, "/GS0 gs 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
		inner := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":  nums(0, 0, 50, 100),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 0 0 50 100 re f")})
		form := maskForm(w, "/GS1 gs /F1 Do",
			reader.Dict{
				"XObject":   reader.Dict{"F1": inner},
				"ExtGState": reader.Dict{"GS1": w.Add(reader.Dict{"ca": nums(0.5)[0]})},
			}, nil)
		return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
			"SMask": reader.Dict{"S": reader.Name("Alpha"), "G": form},
		})}}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 25, 50, color.RGBA{R: 128, G: 128, B: 128, A: 255}, 8)
	wantColour(t, img, 75, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 4)
}

func TestAMaskFormWithNoResourcesUsesThePagesOwn(t *testing.T) {
	// A form need not carry resources; when it does not, what it names is
	// looked for where it was used.
	d := shadedPage(t, "/GS0 gs 0 0 100 100 re f", func(w *reader.Writer) reader.Dict {
		form := maskForm(w, "/S1 sh", nil, nil)
		return reader.Dict{
			"Shading": reader.Dict{"S1": greyRamp(w)},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
				"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form},
			})},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 2, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 10)
	wantColour(t, img, 97, 50, color.RGBA{A: 255}, 10)
}

func TestEverythingIsDrawnThroughTheMask(t *testing.T) {
	// Not only a fill: an image and a gradient are marks like any other and
	// the mask has to reach them too.
	for _, c := range []struct {
		why     string
		content string
	}{
		{"an image", "/GS0 gs q 100 0 0 100 0 0 cm /I1 Do Q"},
		{"a gradient painted on its own", "/GS0 gs /S2 sh"},
	} {
		d := shadedPage(t, c.content, func(w *reader.Writer) reader.Dict {
			form := maskForm(w, "1 g 0 0 50 100 re f", nil, nil)
			return reader.Dict{
				"XObject": reader.Dict{"I1": w.Add(&reader.Stream{Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
					"Width": reader.Integer(1), "Height": reader.Integer(1),
					"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
				}, Raw: []byte{0}})},
				"Shading": reader.Dict{"S2": w.Add(reader.Dict{
					"ShadingType": reader.Integer(2), "ColorSpace": reader.Name("DeviceGray"),
					"Coords": nums(0, 0, 100, 0),
					"Function": w.Add(reader.Dict{"FunctionType": reader.Integer(2),
						"Domain": nums(0, 1), "C0": nums(0), "C1": nums(0), "N": reader.Integer(1)}),
					"Extend": reader.Array{reader.Bool(true), reader.Bool(true)},
				})},
				"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
					"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form},
				})},
			}
		})
		img, err := Page(d, 1, Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got := img.At(25, 50); got != (color.RGBA{A: 255}) {
			t.Errorf("%s: where the mask is open the pixel is %v, wanted it drawn", c.why, got)
		}
		if got := img.At(75, 50); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
			t.Errorf("%s: where the mask is shut the pixel is %v, wanted paper", c.why, got)
		}
	}
}

func TestAGroupIsFadedByTheMaskItIsDrawnUnder(t *testing.T) {
	// A whole group behind a mask: where the mask is open the group goes on
	// as it was drawn, and where it is shut the page is left as it was.
	d := shadedPage(t, "/GS0 gs /F1 Do", func(w *reader.Writer) reader.Dict {
		mask := maskForm(w, "1 g 0 0 50 100 re f", nil, nil)
		form := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":  nums(0, 0, 100, 100),
			"Group": reader.Dict{"S": reader.Name("Transparency")},
		}, Raw: []byte("0 g 0 0 100 100 re f")})
		return reader.Dict{
			"XObject": reader.Dict{"F1": form},
			"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
				"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": mask},
			})},
		}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	wantColour(t, img, 25, 50, color.RGBA{A: 255}, 4)
	wantColour(t, img, 75, 50, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 4)
}

func TestAPageMayNameMoreMasksThanAreWorthKeeping(t *testing.T) {
	// Each mask drawn is a byte for every pixel of the page, so only so many
	// are kept. Past that they are drawn again each time they are used, which
	// costs time and not correctness — the page has to come out the same
	// either way.
	const masks = maxCachedMasks + 4
	content := ""
	for i := 0; i < masks; i++ {
		content += fmt.Sprintf("/GS%d gs 0 %d 100 1 re f ", i, i)
	}
	d := shadedPage(t, content, func(w *reader.Writer) reader.Dict {
		states := reader.Dict{}
		for i := 0; i < masks; i++ {
			// Each mask is its own object, so each is a different one: the
			// left half open for the even ones and the right for the odd.
			at := 0
			if i%2 == 1 {
				at = 50
			}
			form := maskForm(w, fmt.Sprintf("1 g %d 0 50 100 re f", at), nil, nil)
			states[reader.Name(fmt.Sprintf("GS%d", i))] = w.Add(reader.Dict{
				"SMask": w.Add(reader.Dict{"S": reader.Name("Luminosity"), "G": form}),
			})
		}
		return reader.Dict{"ExtGState": states}
	})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The stripe drawn under the last mask is an odd one, so it is on the
	// right and not on the left.
	y := 100 - masks
	wantColour(t, img, 25, y, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 4)
	wantColour(t, img, 75, y, color.RGBA{A: 255}, 4)
}

func TestAMaskNamedInTheMiddleOfSomeTextLeavesThePenWhereItWas(t *testing.T) {
	// A graphics state may be named between one show operator and the next,
	// and a mask with text of its own would move the pen if it were not put
	// back. What follows the mask has to land where it would have anyway.
	page := func(withMask bool) *reader.Document {
		content := "BT /F1 12 Tf 10 50 Td (AB) Tj "
		if withMask {
			content += "/GS0 gs "
		}
		content += "(CD) Tj ET"
		return shadedPage(t, content, func(w *reader.Writer) reader.Dict {
			face := w.Add(reader.Dict{"Type": reader.Name("Font"),
				"Subtype": reader.Name("Type1"), "BaseFont": reader.Name("Helvetica")})
			form := maskForm(w, "BT /F1 12 Tf 80 80 Td (XY) Tj ET 1 g 0 0 100 100 re f",
				reader.Dict{"Font": reader.Dict{"F1": face}}, nil)
			return reader.Dict{
				"Font": reader.Dict{"F1": face},
				"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{
					"SMask": reader.Dict{"S": reader.Name("Luminosity"), "G": form},
				})},
			}
		})
	}
	plain, err := Page(page(false), 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	masked, err := Page(page(true), 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if plain.At(x, y) != masked.At(x, y) {
				t.Fatalf("pixel (%d,%d) is %v without the mask and %v with it; a mask whose "+
					"own paper is white masks nothing and must move nothing",
					x, y, plain.At(x, y), masked.At(x, y))
			}
		}
	}
}
