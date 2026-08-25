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
	wantColour(t, draw(t, d, Options{}), 10, 10, imgcolor.RGBA{0, 0, 255, 255}, 12)
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
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	// A stencil that covers the left half.
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
	wantWhite(t, img, 5, 10)  // hidden by the mask
	wantBlack(t, img, 15, 10) // shown
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

func TestAMaskThatCannotBeRead(t *testing.T) {
	// Neither kind of mask, when it cannot be read, may take the image with
	// it: the image is drawn as it stands.
	for _, entry := range []reader.Name{"SMask", "Mask"} {
		d := imageWithMask(t, entry, reader.Dict{
			"Width": reader.Integer(0), "Height": reader.Integer(0)}, nil)
		wantBlack(t, draw(t, d, Options{}), 10, 10)
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
