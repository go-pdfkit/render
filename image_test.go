package render

import (
	"bytes"
	"image"
	imgcolor "image/color"
	"image/jpeg"
	"testing"

	"github.com/go-pdfkit/reader"
)

// pageWithImage builds a page that draws one image over the whole of it.
func pageWithImage(t *testing.T, dict reader.Dict, data []byte, content string) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	full := reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Image")}
	for k, v := range dict {
		full[k] = v
	}
	xobj := w.Add(&reader.Stream{Dict: full, Raw: data})
	if content == "" {
		content = "q 20 0 0 20 0 0 cm /I Do Q"
	}
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": xobj}},
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

func TestAnImageLandsTheRightWayUp(t *testing.T) {
	// Two by two: red, green on the first row; blue, black on the second. The
	// first row of an image is the top of the square it fills.
	data := []byte{
		255, 0, 0, 0, 255, 0,
		0, 0, 255, 0, 0, 0,
	}
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(2),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceRGB"),
	}, data, "")
	img := draw(t, d, Options{})
	wantColour(t, img, 5, 5, imgcolor.RGBA{255, 0, 0, 255}, 4)
	wantColour(t, img, 15, 5, imgcolor.RGBA{0, 255, 0, 255}, 4)
	wantColour(t, img, 5, 15, imgcolor.RGBA{0, 0, 255, 255}, 4)
	wantColour(t, img, 15, 15, imgcolor.RGBA{0, 0, 0, 255}, 4)
}

func TestAnImageIsPlacedByTheTransform(t *testing.T) {
	// Half the width, in the bottom left quarter of the page.
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
	}, []byte{0}, "q 10 0 0 10 0 0 cm /I Do Q")
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 15)
	wantWhite(t, img, 15, 5)
}

func TestImagesInEveryDepth(t *testing.T) {
	// One row of two greys, at each bit depth the format allows.
	cases := []struct {
		bpc  int
		data []byte
	}{
		{1, []byte{0b01000000}},
		{2, []byte{0b00110000}},
		{4, []byte{0x0F}},
		{8, []byte{0, 255}},
		{16, []byte{0, 0, 255, 255}},
	}
	for _, c := range cases {
		d := pageWithImage(t, reader.Dict{
			"Width": reader.Integer(2), "Height": reader.Integer(1),
			"BitsPerComponent": reader.Integer(c.bpc), "ColorSpace": reader.Name("DeviceGray"),
		}, c.data, "")
		img := draw(t, d, Options{})
		if !isBlack(img, 5, 10) {
			t.Errorf("%d bits: left half is %s", c.bpc, pixel(img, 5, 10))
		}
		if !isWhite(img, 15, 10) {
			t.Errorf("%d bits: right half is %s", c.bpc, pixel(img, 15, 10))
		}
	}
	// A depth the format does not have draws nothing.
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(7), "ColorSpace": reader.Name("DeviceGray"),
	}, []byte{0}, "")
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("an image of seven bits a sample was drawn")
	}
}

func TestAnImageInCMYK(t *testing.T) {
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceCMYK"),
	}, []byte{255, 255, 0, 0}, "")
	// Cyan over magenta prints a violet-blue, not the primary blue the naive
	// algebra gives; poppler draws the same.
	wantColour(t, draw(t, d, Options{}), 10, 10, imgcolor.RGBA{46, 49, 146, 255}, 12)
}

func TestAnIndexedImage(t *testing.T) {
	table := reader.String([]byte{255, 0, 0, 0, 0, 255})
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8),
		"ColorSpace": reader.Array{reader.Name("Indexed"), reader.Name("DeviceRGB"),
			reader.Integer(1), table},
	}, []byte{0, 1}, "")
	img := draw(t, d, Options{})
	wantColour(t, img, 5, 10, imgcolor.RGBA{255, 0, 0, 255}, 4)
	wantColour(t, img, 15, 10, imgcolor.RGBA{0, 0, 255, 255}, 4)
}

func TestADecodeArrayTurnsAnImageInsideOut(t *testing.T) {
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
		"Decode": reader.Array{reader.Integer(1), reader.Integer(0)},
	}, []byte{0}, "")
	// Nothing decodes to everything.
	wantWhite(t, draw(t, d, Options{}), 10, 10)
}

func TestADecodeArrayInShapesNobodyShouldWrite(t *testing.T) {
	for _, decode := range []reader.Object{
		reader.Array{reader.Integer(0)},
		reader.Array{reader.Name("x"), reader.Integer(1)},
		reader.Integer(7),
	} {
		d := pageWithImage(t, reader.Dict{
			"Width": reader.Integer(1), "Height": reader.Integer(1),
			"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
			"Decode": decode,
		}, []byte{0}, "")
		wantBlack(t, draw(t, d, Options{}), 10, 10)
	}
}

func TestAStencilPaintsInTheColourInForce(t *testing.T) {
	// One bit a pixel, two pixels: the first painted, the second not.
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"ImageMask": reader.Bool(true),
	}, []byte{0b01000000}, "1 0 0 rg q 20 0 0 20 0 0 cm /I Do Q")
	img := draw(t, d, Options{})
	wantColour(t, img, 5, 10, imgcolor.RGBA{255, 0, 0, 255}, 4)
	wantWhite(t, img, 15, 10)

	// And a decode array turns which bit means paint.
	d = pageWithImage(t, reader.Dict{
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"ImageMask": reader.Bool(true),
		"Decode":    reader.Array{reader.Integer(1), reader.Integer(0)},
	}, []byte{0b01000000}, "0 g q 20 0 0 20 0 0 cm /I Do Q")
	img = draw(t, d, Options{})
	wantWhite(t, img, 5, 10)
	wantBlack(t, img, 15, 10)

	// A decode array that says nothing useful leaves it alone.
	d = pageWithImage(t, reader.Dict{
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"ImageMask": reader.Bool(true),
		"Decode":    reader.Array{reader.Name("x")},
	}, []byte{0}, "0 g q 20 0 0 20 0 0 cm /I Do Q")
	wantBlack(t, draw(t, d, Options{}), 10, 10)
}

func TestASoftMaskMakesAnImageSeeThrough(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	// A grey mask: half transparent everywhere.
	mask := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
	}, Raw: []byte{128}})
	xobj := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
		"SMask": mask,
	}, Raw: []byte{0}})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 20 0 0 20 0 0 cm /I Do Q")}),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": xobj}},
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
	// Black at half strength on white paper is mid grey.
	wantColour(t, draw(t, d, Options{}), 10, 10, imgcolor.RGBA{128, 128, 128, 255}, 10)
}

func TestAMaskHidesPartOfAnImage(t *testing.T) {
	// A mask sample of 0 means PAINT, so the half whose bit is 0 is the half
	// that shows. This test asserted the complement of that, and so did the
	// code: every explicit mask in the corpus was showing exactly the parts it
	// was meant to hide. Asked the same question — which half of a
	// two-colour page a mask of one 0 bit and one 1 bit paints — poppler
	// answers the 0 half.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	// A stencil whose left half is 0 and right half is 1.
	mask := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"ImageMask": reader.Bool(true),
	}, Raw: []byte{0b01000000}})
	xobj := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(2), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
		"Mask": mask,
	}, Raw: []byte{0, 0}})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 20 0 0 20 0 0 cm /I Do Q")}),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": xobj}},
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
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 10)  // sample 0: painted
	wantWhite(t, img, 15, 10) // sample 1: masked out
}

func TestAJPEGImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.Set(x, y, imgcolor.RGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceRGB"),
		"Filter": reader.Name("DCTDecode"),
	}, buf.Bytes(), "")
	wantColour(t, draw(t, d, Options{}), 10, 10, imgcolor.RGBA{255, 0, 0, 255}, 24)
}

func TestAJPEGThatIsNotOne(t *testing.T) {
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceRGB"),
		"Filter": reader.Name("DCTDecode"),
	}, []byte("not a jpeg at all"), "")
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("something was drawn from bytes that are not a JPEG")
	}
}

func TestAJPEGWhoseSizeDisagreesWithTheDictionary(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.Set(x, y, imgcolor.RGBA{0, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(16), "Height": reader.Integer(16),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceRGB"),
		"Filter": reader.Name("DCTDecode"),
	}, buf.Bytes(), "")
	// The image itself is believed over the dictionary.
	if inked(draw(t, d, Options{})) == 0 {
		t.Error("nothing was drawn")
	}
}

func TestImagesThatAreNotDrawn(t *testing.T) {
	cases := []struct {
		name string
		dict reader.Dict
		data []byte
		body string
	}{
		{"no width", reader.Dict{"Height": reader.Integer(1)}, []byte{0}, ""},
		{"no height", reader.Dict{"Width": reader.Integer(1)}, []byte{0}, ""},
		{"more pixels than there is memory", reader.Dict{
			"Width": reader.Integer(100000), "Height": reader.Integer(100000)}, []byte{0}, ""},
		{"a format nothing here decodes", reader.Dict{
			"Width": reader.Integer(1), "Height": reader.Integer(1),
			"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
			"Filter": reader.Name("JPXDecode")}, []byte{0}, ""},
		{"data that does not decode", reader.Dict{
			"Width": reader.Integer(1), "Height": reader.Integer(1),
			"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
			"Filter": reader.Name("FlateDecode")}, []byte("not deflate"), ""},
		{"a transform that squashes it to nothing", reader.Dict{
			"Width": reader.Integer(1), "Height": reader.Integer(1),
			"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray")},
			[]byte{0}, "q 0 0 0 0 0 0 cm /I Do Q"},
		{"a transform that puts it off the page", reader.Dict{
			"Width": reader.Integer(1), "Height": reader.Integer(1),
			"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray")},
			[]byte{0}, "q 20 0 0 20 500 500 cm /I Do Q"},
	}
	for _, c := range cases {
		d := pageWithImage(t, c.dict, c.data, c.body)
		if got := inked(draw(t, d, Options{})); got != 0 {
			t.Errorf("%s: %d pixels were inked", c.name, got)
		}
	}
}

func TestAnInlineImage(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 1 /BPC 8 /CS /G ID \x00\xff EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 10)
	wantWhite(t, img, 15, 10)
}

func TestAnInlineStencil(t *testing.T) {
	content := "1 0 0 rg q 20 0 0 20 0 0 cm BI /W 2 /H 1 /IM true ID \x40 EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	img := draw(t, d, Options{})
	wantColour(t, img, 5, 10, imgcolor.RGBA{255, 0, 0, 255}, 4)
	wantWhite(t, img, 15, 10)
}

func TestAnInlineImageThatDecodesToNothing(t *testing.T) {
	content := "q 20 0 0 20 0 0 cm BI /W 0 /H 0 /BPC 8 /CS /G ID  EI Q"
	d := onePage(t, [4]float64{0, 0, 20, 20}, content, nil)
	if inked(draw(t, d, Options{})) != 0 {
		t.Error("an image of no size drew something")
	}
}

func TestSampleAtReadsPastTheEnd(t *testing.T) {
	// A row that stops short reads as zero rather than running off.
	for _, bpc := range []int{1, 8, 16} {
		if got := sampleAt(nil, 0, 0, bpc); got != 0 {
			t.Errorf("%d bits from nothing = %d", bpc, got)
		}
	}
	if got := sampleAt([]byte{1}, 0, 0, 16); got != 0 {
		t.Errorf("sixteen bits from one byte = %d", got)
	}
}

// imageWithMask builds a page whose image carries a mask of the given kind,
// with the mask's own dictionary under the caller's control.
func imageWithMask(t *testing.T, entry reader.Name, maskDict reader.Dict, maskData []byte) *reader.Document {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	full := reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Image")}
	for k, v := range maskDict {
		full[k] = v
	}
	mask := w.Add(&reader.Stream{Dict: full, Raw: maskData})
	xobj := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
		entry: mask,
	}, Raw: []byte{0}})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 20 0 0 20 0 0 cm /I Do Q")}),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": xobj}},
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

func TestAMaskThatCannotBeReadTakesTheImageWithIt(t *testing.T) {
	// This reverses what this package used to do, and the corpus is why.
	//
	// It used to draw the image as it stood, on the reasoning that an
	// unreadable mask should not cost you the picture. That is right for a
	// photograph with a soft edge and catastrophic for a scanned page, which
	// is a low-resolution colour background with a HIGH-RESOLUTION BITONAL INK
	// LAYER over it — and that ink layer is a dark rectangle whose shape comes
	// entirely from a JBIG2 stencil. Drawn without the stencil it is a dark
	// rectangle over the whole page.
	//
	// Measured against poppler over 250 scanned medical documents, the median
	// page had 97% of its pixels wrong that way. Not drawing it leaves the
	// background showing, which is what the page mostly is.
	for _, entry := range []reader.Name{"SMask", "Mask"} {
		d := imageWithMask(t, entry, reader.Dict{
			"Width": reader.Integer(0), "Height": reader.Integer(0)}, nil)
		img := draw(t, d, Options{})
		if !isWhite(img, 10, 10) {
			t.Errorf("/%s: the image was drawn although how much of it shows "+
				"is unknown: %s", entry, pixel(img, 10, 10))
		}
	}
}

func TestAnImageUnderAClip(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	xobj := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(1), "Height": reader.Integer(1),
		"BitsPerComponent": reader.Integer(8), "ColorSpace": reader.Name("DeviceGray"),
	}, Raw: []byte{0}})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(20), reader.Integer(20)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("0 0 10 20 re W n q 20 0 0 20 0 0 cm /I Do Q")}),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": xobj}},
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
	img := draw(t, d, Options{})
	wantBlack(t, img, 5, 10)
	wantWhite(t, img, 15, 10)
}

func TestAMaskWhoseBytesAreStillEncodedIsNotDrawn(t *testing.T) {
	// A stencil is one bit a pixel, so the bytes have to be samples. A mask
	// whose filter chain stopped at an image format nothing here decodes is
	// still compressed, and painting it puts noise on the page in the shape of
	// nothing at all.
	//
	// 273 of the image masks in the 1 633 real forms carry an encoded filter.
	// 236 were faxes and nine were JBIG2, and both are decoded before this
	// point now. What can still arrive encoded is a mask a file gave a
	// photographic filter, which no amount of decoding turns into one bit a
	// pixel. Fifty-one first pages of real forms were showing this noise
	// before any of it was decoded.
	for _, tc := range []struct {
		name   string
		filter reader.Name
		drawn  bool
	}{
		{"a mask given a photographic filter", "DCTDecode", false},
		{"no filter at all", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := reader.NewWriter("1.7")
			pagesRef := w.Reserve()
			dict := reader.Dict{"Type": reader.Name("XObject"),
				"Subtype": reader.Name("Image"), "Width": reader.Integer(8),
				"Height": reader.Integer(8), "ImageMask": reader.Bool(true),
				"BitsPerComponent": reader.Integer(1)}
			if tc.filter != "" {
				dict["Filter"] = tc.filter
			}
			// Eight rows of a byte each, every bit zero: as samples that is a
			// solid black stencil, so anything drawn at all is visible.
			img := w.Add(&reader.Stream{Dict: dict, Raw: make([]byte, 8)})
			page := reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
				"MediaBox":  nums(0, 0, 20, 20),
				"Resources": reader.Dict{"XObject": reader.Dict{"M": img}},
				"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
					Raw: []byte("0 g q 20 0 0 20 0 0 cm /M Do Q")})}
			pageRef := w.Add(page)
			w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
				"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
			out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
				"Type": reader.Name("Catalog"), "Pages": pagesRef})})
			if err != nil {
				t.Fatal(err)
			}
			doc, err := reader.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			pic, err := Page(doc, 1, Options{Scale: 1})
			if err != nil {
				t.Fatal(err)
			}
			if drawn := inked(pic) > 0; drawn != tc.drawn {
				t.Errorf("drawn = %v, want %v", drawn, tc.drawn)
			}
		})
	}
}

// TestTheOneByteFormExpandsForAWriter tests a floor rather than a path: nothing
// in the corpus reaches it, because onePerPixel refuses the one-byte form to any
// picture that names a mask or is a stencil, and those are the only things that
// write pixels. It is tested because the floor is what makes that argument safe
// to be wrong about -- a palette is shared between pixels, so writing one pixel
// through it would change every pixel that names the same entry.
func TestTheOneByteFormExpandsForAWriter(t *testing.T) {
	pal := make([]imgcolor.RGBA, 256)
	pal[0] = imgcolor.RGBA{R: 10, G: 20, B: 30, A: 255}
	pal[1] = imgcolor.RGBA{R: 200, G: 210, B: 220, A: 255}
	s := &sampled{w: 2, h: 1, pix: []uint8{0, 1}, pal: pal}

	// Before: one byte a pixel, read through the palette.
	if got := s.at(0, 0); got != pal[0] {
		t.Fatalf("at(0,0) = %+v", got)
	}
	if got := s.at(1, 0); got != pal[1] {
		t.Fatalf("at(1,0) = %+v", got)
	}

	s.expand()
	if s.pal != nil {
		t.Error("the palette survived the expansion")
	}
	if len(s.pix) != 2*1*4 {
		t.Fatalf("pix is %d bytes, want 8", len(s.pix))
	}
	// After: four bytes a pixel, and the same colours.
	if got := s.at(0, 0); got != pal[0] {
		t.Errorf("after expanding, at(0,0) = %+v", got)
	}
	if got := s.at(1, 0); got != pal[1] {
		t.Errorf("after expanding, at(1,0) = %+v", got)
	}

	// And now a writer can change one pixel without touching the other, which
	// is the whole reason the expansion exists.
	paintStencil(s, imgcolor.RGBA{R: 1, G: 2, B: 3, A: 255})
	if got := s.at(0, 0); got.R != 1 || got.A != 255 {
		t.Errorf("paintStencil left %+v", got)
	}

	// Expanding twice is not an error and does not double the buffer.
	s.expand()
	if len(s.pix) != 8 {
		t.Errorf("a second expansion made it %d bytes", len(s.pix))
	}
}

// TestAdoptionTakesOnlyWhatItMayTake covers each reason the decoder's own buffer
// cannot be taken. Getting any of them wrong would not fail loudly: it would hand
// back a picture whose alpha had been read as premultiplied when it was straight,
// or one read past its own rows.
func TestAdoptionTakesOnlyWhatItMayTake(t *testing.T) {
	opaque := func(w, h int) *image.RGBA {
		im := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := range im.Pix {
			im.Pix[i] = 0xff
		}
		return im
	}

	// Taken: dense, from the origin, every pixel opaque.
	im := opaque(3, 2)
	got := adopted(im, 3, 2)
	if got == nil {
		t.Fatal("a dense opaque image was refused")
	}
	if &got.pix[0] != &im.Pix[0] {
		t.Error("the bytes were copied rather than taken")
	}

	// Refused: not an *image.RGBA at all.
	if adopted(image.NewGray(image.Rect(0, 0, 3, 2)), 3, 2) != nil {
		t.Error("a grey image was adopted")
	}

	// Refused: one pixel is not opaque, so premultiplied is not straight.
	tr := opaque(3, 2)
	tr.Pix[4*4+3] = 0x80
	if adopted(tr, 3, 2) != nil {
		t.Error("an image with a translucent pixel was adopted")
	}

	// Refused: a sub-image, whose origin is not (0,0) and whose stride is its
	// parent's.
	parent := opaque(8, 8)
	sub := parent.SubImage(image.Rect(2, 2, 5, 4)).(*image.RGBA)
	if adopted(sub, 3, 2) != nil {
		t.Error("a sub-image was adopted")
	}

	// Refused: the stride is wider than the rows, so the bytes are not dense.
	padded := &image.RGBA{Pix: make([]uint8, 2*16), Stride: 16, Rect: image.Rect(0, 0, 3, 2)}
	for i := range padded.Pix {
		padded.Pix[i] = 0xff
	}
	if adopted(padded, 3, 2) != nil {
		t.Error("a padded image was adopted")
	}

	// Refused: the dimensions asked for are not the ones it has.
	if adopted(opaque(3, 2), 4, 2) != nil {
		t.Error("an image of the wrong width was adopted")
	}

	// Refused: fewer bytes than the dimensions claim.
	short := &image.RGBA{Pix: make([]uint8, 3*2*4-1), Stride: 12, Rect: image.Rect(0, 0, 3, 2)}
	for i := range short.Pix {
		short.Pix[i] = 0xff
	}
	if adopted(short, 3, 2) != nil {
		t.Error("a short buffer was adopted")
	}
}

// TestTheMemoDoesNotDependOnThePacking is a regression test for a defect shipped
// in v0.49.0 and found only by TIMING the corpus: the 256-entry memo was gated on
// the predicate that decides whether a picture may be held as one byte, and that
// predicate refuses a picture with a soft mask. So a masked grey picture converted
// every pixel instead of 256 values -- two forms went from 32 ms to 369 ms with
// byte-identical output, which no proof of pixels can see.
func TestTheMemoDoesNotDependOnThePacking(t *testing.T) {
	for _, c := range []struct {
		name                 string
		n, bpc               int
		bounded, mayPack     bool
		wantMemo, wantPacked bool
	}{
		{"grey, nothing in the way", 1, 8, false, true, true, true},
		{"grey WITH A MASK: memo yes, packed no", 1, 8, false, false, true, false},
		{"grey on the extraction path: memo yes, packed no", 1, 8, true, true, true, false},
		{"grey at 1 bit with a mask", 1, 1, false, false, true, false},
		{"three components: neither", 3, 8, false, true, false, false},
		{"sixteen bits: neither", 1, 16, false, true, false, false},
	} {
		memo, packed := memoAndPack(c.n, c.bpc, c.bounded, c.mayPack)
		if memo != c.wantMemo || packed != c.wantPacked {
			t.Errorf("%s: memo=%v packed=%v, want memo=%v packed=%v",
				c.name, memo, packed, c.wantMemo, c.wantPacked)
		}
	}
	// The property, stated once: packing may be refused, and the memo survives it.
	if memo, _ := memoAndPack(1, 8, false, false); !memo {
		t.Error("refusing the packed form took the memo with it")
	}
}

// TestTheLooseGuardIsTightenedWhereTheCostIsKnown covers the second half of the
// two-stage ceiling. decodeBase cannot know how many components a picture has
// without building its colour space, which for an Indexed space over an ICC
// profile is expensive enough to have cost an eleven-fold regression once. So it
// bounds LOOSELY, at one byte a pixel, and samples() -- which has the space in
// hand already -- applies the real cost.
//
// A picture of 9 000 by 9 000 in three components is 81 megapixels: inside the
// loose bound of 268, and 324 MB at four bytes a pixel, which is past the 256 MB
// the ceiling promises.
func TestTheLooseGuardIsTightenedWhereTheCostIsKnown(t *testing.T) {
	d := pageWithImage(t, reader.Dict{
		"Width": reader.Integer(9000), "Height": reader.Integer(9000),
		"BitsPerComponent": reader.Integer(8),
		"ColorSpace":       reader.Name("DeviceRGB"),
	}, []byte("not the whole picture, and it does not have to be"), "")
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if img == nil {
		t.Fatal("the page came back as nothing")
	}
	// The page is drawn; the picture is not. Nothing to assert about the pixels
	// beyond that it did not allocate 324 MB to find out.
	if isWhite(img, 2, 2) != true {
		t.Error("something was drawn where the refused picture would have gone")
	}
}
