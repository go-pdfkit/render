package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// TestAFaxIsDrawn guards the dependency, not this package's own code.
//
// 67 of the 1 633 real forms in the corpus carry a CCITT-encoded image and 6 of
// their pages have nothing else on them, so those pages drew blank until
// go-pdfkit/reader v0.5.0 decoded the filter. Requiring an older reader here
// would put the blank pages back for anyone who asked only for this package,
// and nothing in this suite would notice: a package's own tests never see its
// callers' module graph.
//
// The fax is built here rather than committed, so nobody else's scan enters the
// repository. It is one Group 4 row, three white pixels then five black, coded
// in horizontal mode: 001 for the mode, then the white run of three and the
// black run of five from Tables 2 and 3 of ITU-T T.4.
func TestAFaxIsDrawn(t *testing.T) {
	bits := "001" + "1000" + "0011"
	data := make([]byte, (len(bits)+7)/8)
	for i, c := range bits {
		if c == '1' {
			data[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	img := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8), "Height": reader.Integer(1),
		"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(1),
		"Filter": reader.Name("CCITTFaxDecode"),
		"DecodeParms": reader.Dict{"K": reader.Integer(-1),
			"Columns": reader.Integer(8), "Rows": reader.Integer(1)},
	}, Raw: data})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 8, 8),
		"Resources": reader.Dict{"XObject": reader.Dict{"F": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 8 0 0 8 0 0 cm /F Do Q")})})
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
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The right five pixels of the row are black and the left three are white.
	// A reader that hands back the fax undecoded draws nothing at all, which is
	// what the six blank pages were.
	if !isWhite(pic, 1, 4) {
		t.Errorf("the white part of the fax is not white: %s", pixel(pic, 1, 4))
	}
	if !isBlack(pic, 6, 4) {
		t.Errorf("the black part of the fax is not black: %s", pixel(pic, 6, 4))
	}
}
