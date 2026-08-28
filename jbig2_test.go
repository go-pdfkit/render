package render

import (
	"errors"
	"testing"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// jbig2Ink is a JBIG2 stream 16 by 8 whose right half is ink. It is written out
// here rather than committed as a file, and it is synthetic: no scan of anyone's
// document enters the repository. It was made by encoding a bitmap with an
// encoder that is not this decoder, and it decodes to the same picture under
// gobig2, under that encoder's own decoder, and under poppler.
var jbig2Ink = []byte{
	0x00, 0x00, 0x00, 0x00, 0x30, 0x00, 0x01, 0x00, 0x00, 0x00, 0x13, 0x00,
	0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00,
	0x01, 0x00, 0x00, 0x00, 0x1e, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00,
	0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x08, 0x03,
	0xff, 0xfd, 0xff, 0x02, 0xfe, 0xfe, 0xfe, 0x8f, 0x66, 0xff, 0xac,
}

// indirect writes any stream inside an object as its own numbered object and
// puts a reference in its place, because that is the only form a real file
// uses: a globals stream is always referred to, never written where it is
// named.
func indirect(wr *reader.Writer, v reader.Object) reader.Object {
	switch o := v.(type) {
	case *reader.Stream:
		return wr.Add(o)
	case reader.Dict:
		d := reader.Dict{}
		for k, e := range o {
			d[k] = indirect(wr, e)
		}
		return d
	case reader.Array:
		a := make(reader.Array, len(o))
		for i, e := range o {
			a[i] = indirect(wr, e)
		}
		return a
	}
	return v
}

// jbig2Page builds a page whose only content is one JBIG2 image filling it.
// extra is merged into the image dictionary, which is how a test says "and this
// one is a stencil" or "and its globals are over there".
func jbig2Page(t *testing.T, data []byte, w, h int, extra reader.Dict) *reader.Document {
	t.Helper()
	wr := reader.NewWriter("1.7")
	pagesRef := wr.Reserve()
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(w), "Height": reader.Integer(h),
		"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(1),
		"Filter": reader.Name("JBIG2Decode"),
	}
	for k, v := range extra {
		dict[k] = indirect(wr, v)
	}
	img := wr.Add(&reader.Stream{Dict: dict, Raw: data})
	pageRef := wr.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 16, 8),
		"Resources": reader.Dict{"XObject": reader.Dict{"S": img}},
		"Contents": wr.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 16 0 0 8 0 0 cm /S Do Q")})})
	wr.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := wr.Finish(reader.Dict{"Root": wr.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAJBIG2ImageIsDrawn(t *testing.T) {
	d := jbig2Page(t, jbig2Ink, 16, 8, nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Ink on the right, paper on the left — so a decoder handing back a flat
	// or a mirrored picture is caught, not merely one handing back nothing.
	if !isBlack(img, 12, 4) {
		t.Errorf("the inked half is not inked: %s", pixel(img, 12, 4))
	}
	if !isWhite(img, 3, 4) {
		t.Errorf("the blank half is not blank: %s", pixel(img, 3, 4))
	}
}

func TestAJBIG2StencilPaintsThroughItsShape(t *testing.T) {
	// This is what JBIG2 nearly always IS. A scanned page's ink layer is a
	// dark rectangle whose shape comes entirely from a JBIG2 stencil; the
	// filter is in twenty documents as content and shapes four thousand
	// images as a mask.
	d := jbig2Page(t, jbig2Ink, 16, 8, reader.Dict{"ImageMask": reader.Bool(true)})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(img, 12, 4) {
		t.Errorf("the stencil did not paint where it has ink: %s", pixel(img, 12, 4))
	}
	if !isWhite(img, 3, 4) {
		t.Errorf("the stencil painted where it has none: %s", pixel(img, 3, 4))
	}
}

func TestAJBIG2StreamThatIsNotOneIsNotDrawn(t *testing.T) {
	// The rule the rest of this file follows: not drawn rather than drawn
	// wrong. Bytes that are not a JBIG2 stream must not become a rectangle.
	d := jbig2Page(t, []byte{0, 1, 2, 3, 4, 5, 6, 7}, 16, 8, nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from bytes that are not a JBIG2 stream", ink)
	}
}

func TestAJBIG2StencilThatWillNotDecodeIsNotDrawn(t *testing.T) {
	d := jbig2Page(t, []byte{0, 1, 2, 3}, 16, 8, reader.Dict{"ImageMask": reader.Bool(true)})
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels painted through a stencil that could not be read", ink)
	}
}

func TestAJBIG2StreamThatIsNotTheSizeTheDictionarySaysIsNotDrawn(t *testing.T) {
	// Unlike a JPEG 2000 codestream, a JBIG2 mask has no size of its own that
	// the page can be laid out from: the geometry was computed from the
	// dictionary. A stream that decodes to another size is not this image.
	d := jbig2Page(t, jbig2Ink, 32, 32, nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from a stream of the wrong size", ink)
	}
}

func TestAJBIG2DecoderThatRefusesTakesTheImageWithIt(t *testing.T) {
	// The decoder refuses a stream whose symbols exceed its resource budget,
	// which seven of four hundred real scanned masks do. Refusing is the right
	// answer; drawing the rectangle anyway is not.
	restore := jbig2Decode
	defer func() { jbig2Decode = restore }()
	jbig2Decode = func(data, globals []byte) (*raster.Image, error) {
		return nil, errors.New("resource budget exceeded")
	}
	d := jbig2Page(t, jbig2Ink, 16, 8, nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn although the decoder refused", ink)
	}
}

func TestAJBIG2DecoderThatReturnsNothingTakesTheImageWithIt(t *testing.T) {
	restore := jbig2Decode
	defer func() { jbig2Decode = restore }()
	jbig2Decode = func(data, globals []byte) (*raster.Image, error) { return nil, nil }
	d := jbig2Page(t, jbig2Ink, 16, 8, nil)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn although the decoder returned no picture", ink)
	}
}

// seenGlobals draws a page and reports the globals the decoder was handed, so
// a test can check the plumbing without needing a stream split into two parts.
func seenGlobals(t *testing.T, parms reader.Object) []byte {
	t.Helper()
	restore := jbig2Decode
	defer func() { jbig2Decode = restore }()
	var got []byte
	var called bool
	jbig2Decode = func(data, globals []byte) (*raster.Image, error) {
		got, called = globals, true
		return restore(data, globals)
	}
	d := jbig2Page(t, jbig2Ink, 16, 8, reader.Dict{"DecodeParms": parms})
	if _, err := Page(d, 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("the decoder was never called")
	}
	return got
}

func TestTheSharedSegmentsAreHandedToTheDecoder(t *testing.T) {
	// An encoder puts the symbol dictionary several pages draw from in a
	// globals stream, and the pages are undecodable without it. /DecodeParms
	// runs parallel to /Filter, so it is one dictionary or an array with one
	// entry per filter in the chain, and both forms occur.
	globals := &reader.Stream{Dict: reader.Dict{}, Raw: []byte("shared segments")}
	for _, tc := range []struct {
		name  string
		parms func(*reader.Stream) reader.Object
	}{
		{"one dictionary", func(s *reader.Stream) reader.Object {
			return reader.Dict{"JBIG2Globals": s}
		}},
		{"an array with one entry per filter", func(s *reader.Stream) reader.Object {
			return reader.Array{reader.Null{}, reader.Dict{"JBIG2Globals": s}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(seenGlobals(t, tc.parms(globals))); got != "shared segments" {
				t.Errorf("the decoder was handed %q, not the globals stream", got)
			}
		})
	}
}

func TestDecodeParametersThatNameNoSharedSegments(t *testing.T) {
	// Every one of these is a stream that decodes on its own, which is what
	// all 403 of the corpus's masks turned out to be. None of them is a
	// reason to refuse the image.
	broken := &reader.Stream{Dict: reader.Dict{"Filter": reader.Name("FlateDecode")},
		Raw: []byte("not deflated")}
	for _, tc := range []struct {
		name  string
		parms reader.Object
	}{
		{"no decode parameters at all", nil},
		{"a dictionary naming none", reader.Dict{"K": reader.Integer(0)}},
		{"an array naming none", reader.Array{reader.Dict{"K": reader.Integer(0)}}},
		{"an array entry that is not a dictionary", reader.Array{reader.Integer(1)}},
		{"globals that are not a stream", reader.Dict{"JBIG2Globals": reader.Integer(1)}},
		{"globals whose own filter breaks", reader.Dict{"JBIG2Globals": broken}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := seenGlobals(t, tc.parms); got != nil {
				t.Errorf("the decoder was handed %q, not nothing", got)
			}
		})
	}
}
