package render

import (
	"testing"

	"github.com/go-opentype/fonts/arimo"
	"github.com/go-pdfkit/reader"
)

func outlinerFor(t *testing.T, d *reader.Document) (*Outliner, reader.Object) {
	t.Helper()
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := d.GetDict(page, "Resources")
	fonts, _ := d.GetDict(res, "Font")
	return NewOutliner(d), fonts.Get("F")
}

func TestAnOutlinerHandsOutTheShapeTheRendererDraws(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 100, 100}, "BT /F 20 Tf 10 10 Td (A) Tj ET",
		reader.Dict{"Resources": reader.Dict{"Font": reader.Dict{"F": reader.Dict{
			"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
			"BaseFont": reader.Name("Helvetica")}}}})
	o, entry := outlinerFor(t, d)
	f, ok := o.Font(entry)
	if !ok {
		t.Fatal("the font was not read")
	}
	if f.Type3() || f.Vertical() || !f.Simple() {
		t.Fatalf("Helvetica read as Type3=%v Vertical=%v Simple=%v", f.Type3(), f.Vertical(), f.Simple())
	}
	codes := f.Codes([]byte("A"))
	if len(codes) != 1 || codes[0] != 'A' {
		t.Fatalf("codes = %v", codes)
	}
	segs, ok := f.Outline('A')
	if !ok || len(segs) == 0 {
		t.Fatal("no outline for A")
	}
	for _, s := range segs {
		for _, p := range s.P {
			if p[0] < -0.5 || p[0] > 1.5 || p[1] < -0.5 || p[1] > 1.5 {
				t.Fatalf("a point of the outline is outside the em: %v", p)
			}
		}
	}
	if again, _ := f.Outline('A'); &again[0] != &segs[0] {
		t.Error("the outline is computed again for the same code")
	}
	if adv := f.Advance('A'); adv < 0.5 || adv > 0.9 {
		t.Errorf("the advance of A is %v of an em", adv)
	}
}

func TestASpaceHasNoOutlineAndTheSameReferenceIsTheSameFont(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 100, 100}, "",
		reader.Dict{"Resources": reader.Dict{"Font": reader.Dict{"F": reader.Dict{
			"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
			"BaseFont": reader.Name("Helvetica"), "Encoding": reader.Name("Identity-V")}}}})
	o, entry := outlinerFor(t, d)
	f, _ := o.Font(entry)
	if _, ok := f.Outline(' '); ok {
		t.Error("a space has an outline")
	}
	// A name that says vertical means nothing to a font of single bytes.
	if f.Vertical() {
		t.Error("a simple font read as vertical")
	}
	if _, ok := f.Type3Proc('A'); ok {
		t.Error("a Type 1 font has a glyph procedure")
	}
}

func TestTheSameReferenceIsTheSameFont(t *testing.T) {
	d := pageWithType3(t, "", "", nil)
	o, entry := outlinerFor(t, d)
	if _, isRef := entry.(reader.Ref); !isRef {
		t.Fatal("the font is not an indirect object here")
	}
	a, _ := o.Font(entry)
	b, _ := o.Font(entry)
	if a.f != b.f {
		t.Error("the font was read twice")
	}
}

func TestAnOutlinerHandsOutAType3Procedure(t *testing.T) {
	d := pageWithType3(t, "BT /F 20 Tf (A) Tj ET", "", nil)
	o, entry := outlinerFor(t, d)
	f, ok := o.Font(entry)
	if !ok || !f.Type3() {
		t.Fatalf("Type 3 font: ok=%v", ok)
	}
	if _, ok := f.Outline('A'); ok {
		t.Error("a Type 3 glyph has no outline")
	}
	proc, ok := f.Type3Proc('A')
	if !ok || len(proc.Raw) == 0 {
		t.Fatal("no procedure for A")
	}
	if _, ok := f.Type3Proc('B'); ok {
		t.Error("a procedure for a code with no glyph")
	}
	if m := f.FontMatrix(); m[0] != 0.001 || m[3] != 0.001 {
		t.Errorf("font matrix = %v", m)
	}
	if f.Resources() != nil {
		t.Error("resources that were not there")
	}
}

func TestAnOutlinerRecognisesAVerticalFont(t *testing.T) {
	for _, tc := range []struct {
		name string
		enc  reader.Object
		want bool
	}{
		{"Identity-V", reader.Name("Identity-V"), true},
		{"Identity-H", reader.Name("Identity-H"), false},
		{"90ms-RKSJ-V", reader.Name("90ms-RKSJ-V"), true},
		{"stream WMode 1", &reader.Stream{Dict: reader.Dict{"WMode": reader.Integer(1)}}, true},
		{"stream WMode 0", &reader.Stream{Dict: reader.Dict{"WMode": reader.Integer(0)}}, false},
		{"none", nil, false},
	} {
		dict := reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("Type0"),
			"BaseFont": reader.Name("X"), "DescendantFonts": reader.Array{}}
		if tc.enc != nil {
			dict["Encoding"] = tc.enc
		}
		// The dictionary is handed over directly: a stream is an indirect object
		// in a file, but only the Encoding entry is read here.
		d := onePage(t, [4]float64{0, 0, 10, 10}, "", nil)
		f, ok := NewOutliner(d).Font(dict)
		if !ok {
			t.Fatalf("%s: not read", tc.name)
		}
		if got := f.Vertical(); got != tc.want {
			t.Errorf("%s: Vertical() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAnOutlinerRefusesWhatIsNotAFont(t *testing.T) {
	d := onePage(t, [4]float64{0, 0, 10, 10}, "", nil)
	if _, ok := NewOutliner(d).Font(reader.Integer(3)); ok {
		t.Error("an integer read as a font")
	}
}

// TestAnOutlinerReportsAndLeansAStandInForBoldAndItalic. A family reduced to
// its regular face stands in for the bold (Embolden) and the italic (the
// outline leans, as the renderer's matrix would lean it).
func TestAnOutlinerReportsAndLeansAStandInForBoldAndItalic(t *testing.T) {
	was := sansStandIn
	t.Cleanup(func() { sansStandIn = was })
	sansStandIn = &family{regular: &substitute{ttf: arimo.TTF}}

	glyph := func(base string) (*GlyphFont, []GlyphSeg) {
		t.Helper()
		d := onePage(t, [4]float64{0, 0, 100, 100}, "BT /F 20 Tf 10 10 Td (H) Tj ET",
			reader.Dict{"Resources": reader.Dict{"Font": reader.Dict{"F": reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
				"BaseFont": reader.Name(base)}}}})
		o, entry := outlinerFor(t, d)
		f, ok := o.Font(entry)
		if !ok {
			t.Fatalf("%s: the font was not read", base)
		}
		segs, ok := f.Outline('H')
		if !ok {
			t.Fatalf("%s: no outline", base)
		}
		return f, segs
	}
	plain, upright := glyph("Helvetica")
	bold, _ := glyph("Helvetica-Bold")
	slanted, leaned := glyph("Helvetica-Oblique")
	if plain.Embolden() || !bold.Embolden() || slanted.Embolden() {
		t.Errorf("Embolden: regular %v, bold %v, oblique %v", plain.Embolden(), bold.Embolden(), slanted.Embolden())
	}
	if len(upright) != len(leaned) {
		t.Fatalf("segments: %d upright, %d leaned", len(upright), len(leaned))
	}
	moved := false
	for i := range upright {
		if upright[i].P != leaned[i].P {
			moved = true
		}
	}
	if !moved {
		t.Error("the stand-in for an italic was not leaned")
	}
}
