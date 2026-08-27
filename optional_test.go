package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// layered builds a one-page document with optional content: the groups the
// test wants, a default configuration, and a page whose content and
// annotations the test supplies. The groups are handed to build so a test can
// name them in a BDC or on an XObject.
func layered(t *testing.T, config func(g []reader.Object) reader.Dict,
	build func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict),
	groups int) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	refs := make([]reader.Object, groups)
	all := reader.Array{}
	for i := range refs {
		refs[i] = w.Add(reader.Dict{"Type": reader.Name("OCG"),
			"Name": reader.String("layer")})
		all = append(all, refs[i])
	}
	content, annots, extra := build(w, refs)
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": nums(0, 0, 100, 100),
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
	}
	if len(annots) > 0 {
		page["Annots"] = annots
	}
	// A BDC names its layer through the page's /Resources/Properties, so every
	// group is listed there under the name the tests use: L0, L1, and so on.
	props := reader.Dict{}
	for i, r := range refs {
		props[reader.Name(string(rune('A'+i)))] = r
	}
	res := reader.Dict{"Properties": props}
	for k, v := range extra {
		res[k] = v
	}
	page["Resources"] = res
	pageRef := w.Add(page)
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	cat := reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef}
	if config != nil {
		cat["OCProperties"] = reader.Dict{"OCGs": all, "D": config(refs)}
	}
	root := w.Add(cat)
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

// square is content that fills the left half of the page in black.
const square = "0 g 0 0 50 100 re f"

func drawLayered(t *testing.T, d *reader.Document, opt Options) int {
	t.Helper()
	if opt.Scale == 0 && opt.DPI == 0 {
		opt.Scale = 1
	}
	img, err := Page(d, 1, opt)
	if err != nil {
		t.Fatal(err)
	}
	return inked(img)
}

func TestALayerThatIsOffIsNotDrawn(t *testing.T) {
	// The whole point: a document says a layer is off, and what is on it does
	// not appear. No file in the corpus exercises this — the layers real
	// documents declare are empty — so the test is the only thing that holds
	// the behaviour up.
	d := layered(t, func(g []reader.Object) reader.Dict {
		return reader.Dict{"OFF": reader.Array{g[0]}}
	}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		return "/OC /A BDC " + square + " EMC", nil, nil
	}, 1)
	if ink := drawLayered(t, d, Options{}); ink != 0 {
		t.Errorf("a layer that is off left %d inked pixels", ink)
	}
	// And the caller who wants everything gets it.
	if ink := drawLayered(t, d, Options{AllLayers: true}); ink == 0 {
		t.Error("AllLayers drew nothing")
	}
}

func TestALayerThatIsOnIsDrawn(t *testing.T) {
	d := layered(t, func(g []reader.Object) reader.Dict {
		return reader.Dict{"ON": reader.Array{g[0]}}
	}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		return "/OC /A BDC " + square + " EMC", nil, nil
	}, 1)
	if ink := drawLayered(t, d, Options{}); ink == 0 {
		t.Error("a layer that is on drew nothing")
	}
}

func TestBaseStateOffHidesEverythingNotNamedOn(t *testing.T) {
	// /BaseState /OFF says the document shows nothing but what /ON lists. All
	// 123 real forms that name a base state name this one, so reading it
	// wrongly would matter the moment one of them put a mark on a layer.
	d := layered(t, func(g []reader.Object) reader.Dict {
		return reader.Dict{"BaseState": reader.Name("OFF"), "ON": reader.Array{g[1]}}
	}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		return "/OC /A BDC 0 g 0 0 50 100 re f EMC " +
			"/OC /B BDC 0 g 50 0 50 100 re f EMC", nil, nil
	}, 2)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isWhite(img, 25, 50) {
		t.Errorf("the layer left off was drawn: %s", pixel(img, 25, 50))
	}
	if !isBlack(img, 75, 50) {
		t.Errorf("the layer named on was not drawn: %s", pixel(img, 75, 50))
	}
}

func TestOffWinsOverOnWhenADocumentSaysBoth(t *testing.T) {
	// The specification leaves this open. Hiding is the safer reading: showing
	// what a document tried to hide is the worse mistake of the two.
	d := layered(t, func(g []reader.Object) reader.Dict {
		return reader.Dict{"ON": reader.Array{g[0]}, "OFF": reader.Array{g[0]}}
	}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		return "/OC /A BDC " + square + " EMC", nil, nil
	}, 1)
	if ink := drawLayered(t, d, Options{}); ink != 0 {
		t.Errorf("a group named both on and off was drawn: %d pixels", ink)
	}
}

func TestNestedMarkedContentInsideAHiddenLayerStaysHidden(t *testing.T) {
	// A layer inside one that is already off cannot turn it back on, and the
	// EMC that closes the inner one must not reveal the outer.
	d := layered(t, func(g []reader.Object) reader.Dict {
		return reader.Dict{"OFF": reader.Array{g[0]}}
	}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		return "/OC /A BDC /Span BMC 0 g 0 0 50 50 re f EMC " +
			"0 g 0 50 50 50 re f EMC", nil, nil
	}, 1)
	if ink := drawLayered(t, d, Options{}); ink != 0 {
		t.Errorf("content inside a hidden layer was drawn: %d pixels", ink)
	}
}

func TestAnEMCWithNoBDCIsHarmless(t *testing.T) {
	// Real content streams are unbalanced. An EMC too many must not take the
	// depth below zero or hide what follows.
	d := layered(t, nil, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		return "EMC EMC " + square, nil, nil
	}, 0)
	if ink := drawLayered(t, d, Options{}); ink == 0 {
		t.Error("an unbalanced EMC hid the rest of the page")
	}
}

func TestAClipInsideAHiddenLayerStillClips(t *testing.T) {
	// Clipping is not marking. A hidden layer that narrows the clip and does
	// not restore it narrows it for what follows, which is what a viewer does
	// and what keeps the graphics state honest.
	d := layered(t, func(g []reader.Object) reader.Dict {
		return reader.Dict{"OFF": reader.Array{g[0]}}
	}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		// The hidden layer clips to the left half; the visible fill covers the
		// whole page and must come out clipped to that half.
		return "/OC /A BDC 0 0 50 100 re W n EMC 0 g 0 0 100 100 re f", nil, nil
	}, 1)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(img, 25, 50) {
		t.Errorf("inside the clip is bare: %s", pixel(img, 25, 50))
	}
	if !isWhite(img, 75, 50) {
		t.Errorf("the clip set inside a hidden layer did not narrow: %s", pixel(img, 75, 50))
	}
}

// TestAFormOnALayerThatIsOffIsNotDrawn covers the /OC an XObject carries on
// itself, rather than a BDC around the Do that draws it. 155 XObjects on the
// first three pages of the 1 633 real forms carry one.
func TestAFormOnALayerThatIsOffIsNotDrawn(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  bool
		want bool
	}{{"off", true, false}, {"on", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			d := layered(t, func(g []reader.Object) reader.Dict {
				if tc.off {
					return reader.Dict{"OFF": reader.Array{g[0]}}
				}
				return reader.Dict{}
			}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
				form := w.Add(&reader.Stream{Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
					"BBox": nums(0, 0, 50, 100), "OC": g[0],
				}, Raw: []byte(square)})
				return "/F Do", nil, reader.Dict{"XObject": reader.Dict{"F": form}}
			}, 1)
			if drawn := drawLayered(t, d, Options{}) > 0; drawn != tc.want {
				t.Errorf("drawn = %v, want %v", drawn, tc.want)
			}
		})
	}
}

// TestTheMarkingOperatorsAreAllSuppressed walks the operators that put
// something on the page but do not go through the path painter: drawing a form,
// a shading and an inline image. Each has its own early return.
func TestTheMarkingOperatorsAreAllSuppressed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		extra   func(w *reader.Writer) reader.Dict
	}{
		{"Do", "/OC /A BDC /F Do EMC", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"XObject": reader.Dict{"F": w.Add(&reader.Stream{
				Dict: reader.Dict{"Type": reader.Name("XObject"),
					"Subtype": reader.Name("Form"), "BBox": nums(0, 0, 100, 100)},
				Raw: []byte(square)})}}
		}},
		{"sh", "/OC /A BDC /S sh EMC", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Shading": reader.Dict{"S": reader.Dict{
				"ShadingType": reader.Integer(2),
				"ColorSpace":  reader.Name("DeviceGray"),
				"Coords":      nums(0, 0, 100, 0),
				"Function": reader.Dict{"FunctionType": reader.Integer(2),
					"Domain": nums(0, 1), "C0": nums(0), "C1": nums(0),
					"N": reader.Integer(1)},
			}}}
		}},
		{"BI", "/OC /A BDC q 100 0 0 100 0 0 cm BI /W 1 /H 1 /CS /G /BPC 8 " +
			"ID \x00 EI Q EMC", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := layered(t, func(g []reader.Object) reader.Dict {
				return reader.Dict{"OFF": reader.Array{g[0]}}
			}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
				var extra reader.Dict
				if tc.extra != nil {
					extra = tc.extra(w)
				}
				return tc.content, nil, extra
			}, 1)
			if ink := drawLayered(t, d, Options{}); ink != 0 {
				t.Errorf("%s drew %d pixels inside a layer that is off", tc.name, ink)
			}
			// And the same content with the layer on must draw something, or
			// the test would pass for the wrong reason.
			e := layered(t, func(g []reader.Object) reader.Dict {
				return reader.Dict{}
			}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
				var extra reader.Dict
				if tc.extra != nil {
					extra = tc.extra(w)
				}
				return tc.content, nil, extra
			}, 1)
			if ink := drawLayered(t, e, Options{}); ink == 0 {
				t.Errorf("%s drew nothing even with the layer on", tc.name)
			}
		})
	}
}

// TestAnAnnotationOnALayerThatIsOffIsNotDrawn covers the /OC an annotation
// carries, which is how a whole block of a form's fields is put on a layer.
// 1 373 annotations on the first three pages of the 1 633 real forms name one.
func TestAnAnnotationOnALayerThatIsOffIsNotDrawn(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  bool
		want bool
	}{{"off", true, false}, {"on", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			d := layered(t, func(g []reader.Object) reader.Dict {
				if tc.off {
					return reader.Dict{"OFF": reader.Array{g[0]}}
				}
				return reader.Dict{}
			}, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
				return "", reader.Array{w.Add(reader.Dict{
					"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
					"Rect": nums(10, 10, 30, 30), "OC": g[0],
					"AP": reader.Dict{"N": w.Add(&reader.Stream{
						Dict: reader.Dict{"Type": reader.Name("XObject"),
							"Subtype": reader.Name("Form"), "BBox": nums(0, 0, 20, 20)},
						Raw: []byte("0 g 0 0 20 20 re f")})},
				})}, nil
			}, 1)
			if drawn := drawLayered(t, d, Options{}) > 0; drawn != tc.want {
				t.Errorf("drawn = %v, want %v", drawn, tc.want)
			}
		})
	}
}

// ocDoc builds a document whose catalogue holds whatever /OCProperties the
// test wants, and hands back the groups so a test can name them directly. The
// tests below reach into optional's own methods, because the branches they
// cover — a membership policy, a malformed entry — are easier to state as
// questions to the reader than as pages to draw.
func ocDoc(t *testing.T, props func(g []reader.Object) reader.Object, groups int) (*reader.Document, []reader.Object) {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	refs := make([]reader.Object, groups)
	for i := range refs {
		refs[i] = w.Add(reader.Dict{"Type": reader.Name("OCG")})
	}
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": nums(0, 0, 10, 10)})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	cat := reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef}
	if props != nil {
		cat["OCProperties"] = props(refs)
	}
	out, err := w.Finish(reader.Dict{"Root": w.Add(cat)})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d, refs
}

func TestOptionalContentReadsNothingFromADocumentThatSaysNothing(t *testing.T) {
	// Three ways a document can have no default configuration to read: no
	// /OCProperties at all, an /OCProperties that is not a dictionary, and one
	// with no /D. None of them may hide anything.
	for _, tc := range []struct {
		name  string
		props func(g []reader.Object) reader.Object
	}{
		{"none", nil},
		{"not a dictionary", func(g []reader.Object) reader.Object { return reader.Integer(7) }},
		{"no default configuration", func(g []reader.Object) reader.Object {
			return reader.Dict{"OCGs": reader.Array{g[0]}}
		}},
		{"default configuration is not a dictionary", func(g []reader.Object) reader.Object {
			return reader.Dict{"OCGs": reader.Array{g[0]}, "D": reader.Name("no")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, refs := ocDoc(t, tc.props, 1)
			o := readOptional(d)
			if len(o.off) != 0 {
				t.Errorf("%d groups hidden, want none", len(o.off))
			}
			if o.hidden(d, refs[0]) {
				t.Error("a group is hidden by a document that hides nothing")
			}
		})
	}
}

func TestAMembershipDictionaryCombinesItsGroups(t *testing.T) {
	// /P says what makes the content visible. The four policies are the whole
	// mechanism, and /AnyOn is what a document that names none of them means.
	d, refs := ocDoc(t, func(g []reader.Object) reader.Object {
		return reader.Dict{"OCGs": reader.Array{g[0], g[1]},
			"D": reader.Dict{"OFF": reader.Array{g[0]}}}
	}, 2)
	o := readOptional(d)
	on, off := refs[1], refs[0]
	for _, tc := range []struct {
		policy string
		groups reader.Array
		hidden bool
	}{
		{"", reader.Array{off, on}, false},        // AnyOn: one is on
		{"", reader.Array{off}, true},             // AnyOn: none is on
		{"AllOn", reader.Array{off, on}, true},    // one is off
		{"AllOn", reader.Array{on, on}, false},    // none is off
		{"AnyOff", reader.Array{off, on}, false},  // one is off
		{"AnyOff", reader.Array{on, on}, true},    // none is off
		{"AllOff", reader.Array{off, on}, true},   // one is on
		{"AllOff", reader.Array{off, off}, false}, // none is on
	} {
		md := reader.Dict{"Type": reader.Name("OCMD"), "OCGs": tc.groups}
		if tc.policy != "" {
			md["P"] = reader.Name(tc.policy)
		}
		policy := tc.policy
		if policy == "" {
			policy = "AnyOn (the default)"
		}
		if got := o.hidden(d, md); got != tc.hidden {
			t.Errorf("%s over %d groups: hidden = %v, want %v",
				policy, len(tc.groups), got, tc.hidden)
		}
	}
	// A membership dictionary naming no group hides nothing: there is nothing
	// for the policy to be about.
	if o.hidden(d, reader.Dict{"Type": reader.Name("OCMD")}) {
		t.Error("a membership dictionary with no groups hid its content")
	}
	// One group written without an array is the commoner form.
	if !o.hidden(d, reader.Dict{"Type": reader.Name("OCMD"), "OCGs": off}) {
		t.Error("a single group off did not hide its content")
	}
	if o.hidden(d, reader.Dict{"Type": reader.Name("OCMD"), "OCGs": on}) {
		t.Error("a single group on hid its content")
	}
	// A visibility expression is not read, and a document that has one is
	// treated as visible rather than guessed at.
	if o.hidden(d, reader.Dict{"Type": reader.Name("OCMD"), "OCGs": reader.Array{off},
		"VE": reader.Array{reader.Name("Not")}}) != true {
		t.Error("VE changed the answer, which it is documented not to do")
	}
	// An entry that is not a dictionary at all.
	if o.hidden(d, reader.Integer(3)) {
		t.Error("a number hid something")
	}
	// A group written out in place rather than referenced cannot be named in
	// /ON or /OFF, so it is shown.
	if o.hidden(d, reader.Dict{"Type": reader.Name("OCG")}) {
		t.Error("a group written in place was hidden")
	}
	// An /OCGs that is neither a reference nor an array names no group.
	if o.hidden(d, reader.Dict{"Type": reader.Name("OCMD"), "OCGs": reader.Integer(1)}) {
		t.Error("a malformed /OCGs hid its content")
	}
}

func TestABDCThatNamesNoLayerHidesNothing(t *testing.T) {
	d, refs := ocDoc(t, func(g []reader.Object) reader.Object {
		return reader.Dict{"OCGs": reader.Array{g[0]},
			"D": reader.Dict{"OFF": reader.Array{g[0]}}}
	}, 1)
	r := &renderer{doc: d, oc: readOptional(d)}
	res := reader.Dict{"Properties": reader.Dict{"A": refs[0]}}
	for _, tc := range []struct {
		name     string
		operands []reader.Object
		res      reader.Dict
		hidden   bool
	}{
		{"no operands", nil, res, false},
		{"one operand", []reader.Object{reader.Name("OC")}, res, false},
		{"a tag that is not OC",
			[]reader.Object{reader.Name("Span"), reader.Name("A")}, res, false},
		{"a name with no Properties to look it up in",
			[]reader.Object{reader.Name("OC"), reader.Name("A")}, reader.Dict{}, false},
		{"a name that is there",
			[]reader.Object{reader.Name("OC"), reader.Name("A")}, res, true},
		{"a name that is not there",
			[]reader.Object{reader.Name("OC"), reader.Name("Z")}, res, false},
		{"the dictionary written in place instead of a name",
			[]reader.Object{reader.Name("OC"), refs[0]}, res, true},
	} {
		if got := r.hiddenProperty(tc.operands, tc.res); got != tc.hidden {
			t.Errorf("%s: hidden = %v, want %v", tc.name, got, tc.hidden)
		}
	}
}
