package render

import (
	"encoding/binary"
	"image/color"
	"math"
	"testing"

	"github.com/go-pdfkit/reader"
)

// iccProfile writes a real ICC byte stream, so these tests exercise the
// reading as well as the dispatch. tags is a list of (signature, data).
func iccProfile(space string, tags [][2]any) []byte {
	head := make([]byte, 132)
	copy(head[16:20], space)
	copy(head[20:24], "XYZ ")
	copy(head[36:40], "acsp")
	binary.BigEndian.PutUint32(head[128:], uint32(len(tags)))
	table := make([]byte, len(tags)*12)
	off := len(head) + len(table)
	var body []byte
	for i, t := range tags {
		sig, data := t[0].(string), t[1].([]byte)
		copy(table[i*12:], sig)
		binary.BigEndian.PutUint32(table[i*12+4:], uint32(off+len(body)))
		binary.BigEndian.PutUint32(table[i*12+8:], uint32(len(data)))
		body = append(body, data...)
	}
	out := append(append(head, table...), body...)
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	return out
}

func iccXYZTag(x, y, z float64) []byte {
	d := make([]byte, 20)
	copy(d, "XYZ ")
	for i, v := range []float64{x, y, z} {
		binary.BigEndian.PutUint32(d[8+i*4:], uint32(int32(math.Round(v*65536))))
	}
	return d
}

func iccGammaTag(g float64) []byte {
	d := make([]byte, 14)
	copy(d, "curv")
	binary.BigEndian.PutUint32(d[8:], 1)
	binary.BigEndian.PutUint16(d[12:], uint16(math.Round(g*256)))
	return d
}

func iccOpaqueTag(typ string, n int) []byte {
	d := make([]byte, 8+n)
	copy(d, typ)
	return d
}

// appleRGBProfile is the profile carried by ia-medical's
// 2011001RegenerativeEndodonticsPart2.pdf: gamma 1.8008 and colorants summing
// to D50. A real document's real profile, not one chosen to be easy.
func appleRGBProfile() []byte {
	return iccProfile("RGB ", [][2]any{
		{"wtpt", iccXYZTag(0.950455, 1.0, 1.089050)},
		{"rTRC", iccGammaTag(1.8008)}, {"gTRC", iccGammaTag(1.8008)}, {"bTRC", iccGammaTag(1.8008)},
		{"rXYZ", iccXYZTag(0.475540, 0.255157, 0.018448)},
		{"gXYZ", iccXYZTag(0.339722, 0.672592, 0.113327)},
		{"bXYZ", iccXYZTag(0.148956, 0.072250, 0.693115)},
	})
}

// iccSpaceOf builds the [/ICCBased <stream>] array a file writes and resolves
// it the way a page would.
func iccSpaceOf(t *testing.T, profile []byte, n int) *space {
	t.Helper()
	w := reader.NewWriter("1.7")
	dict := reader.Dict{}
	if n > 0 {
		dict["N"] = reader.Integer(n)
	}
	ref := w.Add(&reader.Stream{Dict: dict, Raw: profile})
	pagesRef := w.Reserve()
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": nums(0, 0, 4, 4),
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
	r := &renderer{doc: d}
	return r.colourSpaceArray("ICCBased", reader.Array{reader.Name("ICCBased"), ref}, nil, 0)
}

func TestAnICCProfileThatIsArithmeticIsRead(t *testing.T) {
	s := iccSpaceOf(t, appleRGBProfile(), 3)
	if s.name != "ICCBased" || s.components != 3 {
		t.Fatalf("space = %q with %d components, want ICCBased with 3", s.name, s.components)
	}
	// poppler, which is little-cms, draws these samples through this profile
	// as the values on the right.
	for _, c := range []struct{ in, want [3]int }{
		{[3]int{255, 0, 0}, [3]int{255, 43, 6}},
		{[3]int{0, 0, 255}, [3]int{25, 34, 251}},
		{[3]int{104, 104, 103}, [3]int{123, 123, 122}},
		{[3]int{255, 255, 255}, [3]int{255, 255, 255}},
	} {
		got := s.convert([]float64{float64(c.in[0]) / 255, float64(c.in[1]) / 255, float64(c.in[2]) / 255})
		for i, v := range [3]uint8{got.R, got.G, got.B} {
			if d := int(v) - c.want[i]; d > 1 || d < -1 {
				t.Errorf("%v channel %d = %d, little-cms says %d", c.in, i, v, c.want[i])
			}
		}
	}
	// And it is NOT a pass-through, which is what it replaces.
	if got := s.convert([]float64{0.5, 0.5, 0.5}); got == deviceRGB.convert([]float64{0.5, 0.5, 0.5}) {
		t.Error("the profile gave the same answer as DeviceRGB; it was not read")
	}
}

func TestAGreyICCProfileIsRead(t *testing.T) {
	p := iccProfile("GRAY", [][2]any{
		{"wtpt", iccXYZTag(0.9642, 1.0, 0.8249)},
		{"kTRC", iccGammaTag(2.2)},
	})
	s := iccSpaceOf(t, p, 1)
	if s.name != "ICCBased" || s.components != 1 {
		t.Fatalf("space = %q with %d components, want ICCBased with 1", s.name, s.components)
	}
	if got := s.convert([]float64{1}); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Errorf("full scale = %v, want white", got)
	}
	if got := s.convert([]float64{0}); got != (color.RGBA{A: 255}) {
		t.Errorf("nought = %v, want black", got)
	}
}

// TestAProfileThatNeedsAnEngineFallsBackToTheComponentCount. Declining is the
// point: a lookup-table profile approximated by a matrix would be wrong
// silently, so render reads the samples as it did before and the conformance
// bucket still counts the picture apart.
func TestAProfileThatNeedsAnEngineFallsBackToTheComponentCount(t *testing.T) {
	for name, tc := range map[string]struct {
		profile []byte
		n       int
		want    *space
		args    []float64
	}{
		"a lookup-table transform": {
			iccProfile("CMYK", [][2]any{{"A2B0", iccOpaqueTag("mft2", 64)}}), 4,
			deviceCMYK, []float64{0, 0, 0, 1}},
		"a parametric curve": {
			iccProfile("RGB ", [][2]any{
				{"rXYZ", iccXYZTag(0.4, 0.2, 0)}, {"gXYZ", iccXYZTag(0.3, 0.7, 0.1)},
				{"bXYZ", iccXYZTag(0.2, 0.1, 0.7)},
				{"rTRC", iccOpaqueTag("para", 8)}, {"gTRC", iccOpaqueTag("para", 8)},
				{"bTRC", iccOpaqueTag("para", 8)}}), 3,
			deviceRGB, []float64{0.5, 0.25, 0.75}},
		"bytes that are not a profile": {[]byte("not an ICC profile at all"), 3,
			deviceRGB, []float64{0.5, 0.25, 0.75}},
		"an empty stream": {nil, 1, deviceGray, []float64{0.5}},
		"a profile that disagrees with /N": {appleRGBProfile(), 1,
			deviceGray, []float64{0.5}},
		"a grey profile where /N says three": {
			iccProfile("GRAY", [][2]any{
				{"wtpt", iccXYZTag(0.9642, 1.0, 0.8249)}, {"kTRC", iccGammaTag(2.2)}}), 3,
			deviceRGB, []float64{0.5, 0.25, 0.75}},
	} {
		got := iccSpaceOf(t, tc.profile, tc.n)
		if got.convert(tc.args) != tc.want.convert(tc.args) {
			t.Errorf("%s: did not fall back to %s (%v vs %v)",
				name, tc.want.name, got.convert(tc.args), tc.want.convert(tc.args))
		}
	}
}

// TestAnICCSpaceWithNothingAfterItsName. [/ICCBased] alone, and a profile
// with no /N that cannot be read: there is nothing to count components by, so
// the format's own fallback of three applies.
func TestAnICCSpaceWithNothingAfterItsName(t *testing.T) {
	r := &renderer{}
	got := r.colourSpaceArray("ICCBased", reader.Array{reader.Name("ICCBased")}, nil, 0)
	v := []float64{0.5, 0.25, 0.75}
	if got.convert(v) != deviceRGB.convert(v) {
		t.Errorf("a bare /ICCBased did not fall back to DeviceRGB")
	}
	if s := iccSpaceOf(t, []byte("rubbish"), 0); s.convert(v) != deviceRGB.convert(v) {
		t.Errorf("an unreadable profile with no /N did not fall back to DeviceRGB")
	}
}
