package render

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/go-pdfkit/reader"
)

// truncatedFlate compresses s and then cuts the result, so inflating it yields
// a prefix of s and then fails — which is what a damaged stream in a real file
// looks like.
func truncatedFlate(t *testing.T, s string, keep int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if keep >= len(b) {
		t.Fatalf("keeping %d of %d bytes cuts nothing", keep, len(b))
	}
	return b[:keep]
}

func TestAPageWhoseContentIsDamagedIsDrawnAsFarAsItDecoded(t *testing.T) {
	// A page of many squares, compressed and then cut. The first squares are
	// in the part that inflates; the rest are not. Drawing nothing at all
	// would be the old behaviour and is the wrong one: 263 streams in 212 of
	// the 1 633 real forms cannot be decoded cleanly.
	content := "0 g"
	for i := 0; i < 200; i++ {
		content += " 0 0 10 10 re f 10 0 10 10 re f 20 0 10 10 re f"
	}
	raw := truncatedFlate(t, content, 40)

	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": nums(0, 0, 40, 20),
		"Contents": w.Add(&reader.Stream{
			Dict: reader.Dict{"Filter": reader.Name("FlateDecode")}, Raw: raw})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatalf("a damaged page came back as an error: %v", err)
	}
	if inked(img) == 0 {
		t.Error("a damaged page drew nothing at all")
	}
}

func TestAFormWhoseFilterIsUnknownIsNotDrawn(t *testing.T) {
	// A filter nothing can apply leaves no bytes any filter decoded, so there
	// is nothing to draw — and the encoded bytes must not be run as operators.
	d := layered(t, nil, func(w *reader.Writer, g []reader.Object) (string, reader.Array, reader.Dict) {
		form := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox": nums(0, 0, 100, 100), "Filter": reader.Name("NoSuchDecode"),
		}, Raw: []byte("0 g 0 0 100 100 re f")})
		return "/F Do", nil, reader.Dict{"XObject": reader.Dict{"F": form}}
	}, 0)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ink := inked(img); ink != 0 {
		t.Errorf("%d pixels drawn from a stream no filter could decode", ink)
	}
}
