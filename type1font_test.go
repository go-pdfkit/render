package render

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/go-opentype/fonts"
	"github.com/go-pdfkit/reader"
)

// The eexec cipher a Type 1 program is written in.
const (
	eexecC1  = 52845
	eexecC2  = 22719
	eexecKey = 55665
	csKey    = 4330
)

// eexecEncrypt writes a section the way a Type 1 program carries it.
func eexecEncrypt(plain []byte, key uint16, lead int) []byte {
	r := key
	buf := append(bytes.Repeat([]byte{'X'}, lead), plain...)
	out := make([]byte, 0, len(buf))
	for _, p := range buf {
		c := p ^ byte(r>>8)
		r = (uint16(c)+r)*eexecC1 + eexecC2
		out = append(out, c)
	}
	return out
}

// t1num encodes numbers the way a Type 1 charstring carries them.
func t1num(vs ...int) []byte {
	var out []byte
	for _, v := range vs {
		switch {
		case v >= -107 && v <= 107:
			out = append(out, byte(v+139))
		case v >= 108 && v <= 1131:
			v -= 108
			out = append(out, byte(v/256+247), byte(v%256))
		default:
			out = append(out, 255, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		}
	}
	return out
}

// t1Box is a charstring drawing a filled square of the given width.
func t1Box(width int) []byte {
	cs := append(t1num(0, width), 13) // hsbw
	cs = append(cs, t1num(50, 50)...)
	cs = append(cs, 21) // rmoveto
	cs = append(cs, t1num(400)...)
	cs = append(cs, 6) // hlineto
	cs = append(cs, t1num(400)...)
	cs = append(cs, 7) // vlineto
	cs = append(cs, t1num(-400)...)
	cs = append(cs, 6, 9, 14) // hlineto closepath endchar
	return cs
}

// type1Program builds a whole Type 1 font program naming one glyph, with the
// encoding it says it was written with.
func type1Program(glyphName string, builtIn map[byte]string) []byte {
	var clear bytes.Buffer
	clear.WriteString("%!PS-AdobeFont-1.0: Synthetic 001.001\n/FontName /Synthetic def\n")
	clear.WriteString("/FontMatrix [0.001 0 0 0.001 0 0] readonly def\n")
	if builtIn == nil {
		clear.WriteString("/Encoding StandardEncoding def\n")
	} else {
		clear.WriteString("/Encoding 256 array\n")
		for code := 0; code < 256; code++ {
			if name, ok := builtIn[byte(code)]; ok {
				fmt.Fprintf(&clear, "dup %d /%s put\n", code, name)
			}
		}
		clear.WriteString("readonly def\n")
	}
	clear.WriteString("currentdict end\ncurrentfile eexec\n")

	enc := eexecEncrypt(t1Box(600), csKey, 4)
	notdef := eexecEncrypt([]byte{14}, csKey, 4)
	var priv bytes.Buffer
	priv.WriteString("XXXX dup /Private 8 dict dup begin\n/lenIV 4 def\n")
	fmt.Fprintf(&priv, "/CharStrings 2 dict dup begin\n")
	fmt.Fprintf(&priv, "/.notdef %d RD ", len(notdef))
	priv.Write(notdef)
	priv.WriteString(" ND\n")
	fmt.Fprintf(&priv, "/%s %d RD ", glyphName, len(enc))
	priv.Write(enc)
	priv.WriteString(" ND\nend\nend\n")
	return append(clear.Bytes(), eexecEncrypt(priv.Bytes(), eexecKey, 4)...)
}

// pageWithType1 builds a page whose font is a Type 1 program, with whatever
// /Encoding the document wants to give it.
func pageWithType1(t *testing.T, content, glyphName string, builtIn map[byte]string, encoding reader.Object) *reader.Document {
	t.Helper()
	prog := type1Program(glyphName, builtIn)
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	file := w.Add(&reader.Stream{
		Dict: reader.Dict{"Length1": reader.Integer(len(prog))},
		Raw:  prog,
	})
	descriptor := w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Synthetic"),
		"Flags": reader.Integer(32), "FontFile": file,
	})
	widths := make(reader.Array, 0, 224)
	for i := 32; i < 256; i++ {
		widths = append(widths, reader.Integer(600))
	}
	font := reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
		"BaseFont": reader.Name("Synthetic"), "FirstChar": reader.Integer(32),
		"LastChar": reader.Integer(255), "Widths": widths,
		"FontDescriptor": descriptor,
	}
	if encoding != nil {
		font["Encoding"] = encoding
	}
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(60)},
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
		"Resources": reader.Dict{"Font": reader.Dict{"F1": w.Add(font)}},
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{w.Add(page)}, "Count": reader.Integer(1)})
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

func TestAPageSetInATypeOneProgram(t *testing.T) {
	// A Type 1 program is addressed by the name the document's encoding
	// gives a code. Here code 65 is named A, and the program has an A.
	d := pageWithType1(t, "BT /F1 40 Tf 10 10 Td (A) Tj ET", "A", nil,
		reader.Dict{"Type": reader.Name("Encoding"),
			"Differences": reader.Array{reader.Integer(65), reader.Name("A")}})
	img, err := Page(d, 1, Options{Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	if inked(img) == 0 {
		t.Error("a page set in a Type 1 program drew nothing")
	}
}

func TestATypeOneProgramReadThroughItsOwnEncoding(t *testing.T) {
	// The document says nothing about how the font is encoded, so the
	// program's own encoding is what says which glyph a byte is. Here it
	// says code 65 is the glyph called "weird", which nothing else would
	// have found.
	d := pageWithType1(t, "BT /F1 40 Tf 10 10 Td (A) Tj ET", "weird",
		map[byte]string{65: "weird"}, nil)
	img, err := Page(d, 1, Options{Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	if inked(img) == 0 {
		t.Error("a program read through its own encoding drew nothing")
	}
}

func TestWhichParserAProgramIsGivenTo(t *testing.T) {
	// The key a program arrives under says what it is — except FontFile3,
	// which is either a whole OpenType font or a bare CFF one, so both are
	// tried.
	ttf := fonts.MostLegible()
	cases := []struct {
		name string
		key  reader.Name
		data []byte
		ok   bool
	}{
		{"a TrueType font under FontFile2", "FontFile2", ttf, true},
		{"an OpenType font under FontFile3", "FontFile3", ttf, true},
		{"a Type 1 program under FontFile", "FontFile", type1Program("A", nil), true},
		{"a TrueType font under FontFile", "FontFile", ttf, false},
		{"nonsense under FontFile3", "FontFile3", []byte("not a font at all"), false},
		{"nonsense under FontFile2", "FontFile2", []byte("not a font at all"), false},
	}
	for _, c := range cases {
		f, err := readProgram(c.key, c.data)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
			continue
		}
		if c.ok && f.NumGlyphs() == 0 {
			t.Errorf("%s: read a font with no glyphs", c.name)
		}
	}
}
