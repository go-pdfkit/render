package render

import (
	"github.com/go-gfx/gfx/geometry"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/reader"
)

// A fontKind says how a font's glyphs are found and drawn.
type fontKind uint8

const (
	// simpleFont is addressed a byte at a time and draws from a font program.
	simpleFont fontKind = iota
	// compositeFont is addressed by character identifier, usually two bytes.
	compositeFont
	// type3Font draws each glyph by running a little content stream.
	type3Font
)

// A pdfFont is everything needed to draw the glyphs a content stream names:
// how its codes are read, how wide each one is, and where its outlines come
// from.
type pdfFont struct {
	kind fontKind

	// program is the embedded font, when there is one, and face is the handle
	// its outlines come through. Outlines arrive in font units.
	program *opentype.Font
	face    *opentype.Face
	perEm   float64

	// widths are in text space, a thousandth of the size, by code.
	widths       map[int]float64
	defaultWidth float64

	// names is what each code is called, for a font addressed by glyph name.
	names map[int]string
	// symbolic fonts are addressed through the font's own character map
	// rather than through an encoding of names.
	symbolic bool

	// cidToGID maps a character identifier to a glyph, for a composite font
	// that says the two are not the same.
	cidToGID []byte

	// The pieces a Type 3 font draws with.
	charProcs   reader.Dict
	fontMatrix  geometry.Matrix
	t3Resources reader.Dict
}

// codes splits a string into the codes a font is addressed by, along with the
// byte each one started at, which is what decides whether word spacing
// applies.
func (f *pdfFont) codes(s []byte) []int {
	if f.kind != compositeFont {
		out := make([]int, len(s))
		for i, b := range s {
			out[i] = int(b)
		}
		return out
	}
	// Every composite encoding seen in the wild is two bytes wide.
	out := make([]int, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		out = append(out, int(s[i])<<8|int(s[i+1]))
	}
	return out
}

// width is how far the pen moves for one code, in text space.
func (f *pdfFont) width(code int) float64 {
	if w, ok := f.widths[code]; ok {
		return w
	}
	return f.defaultWidth
}

// glyph is the outline of one code, in text space — a thousandth of the point
// size, the units a PDF font is measured in.
func (f *pdfFont) glyph(code int) ([]opentype.Segment, bool) {
	if f.face == nil {
		return nil, false
	}
	return f.face.GlyphOutline(f.glyphIndex(code))
}

// glyphIndex works out which glyph of the font program a code stands for.
func (f *pdfFont) glyphIndex(code int) opentype.GlyphIndex {
	if f.kind == compositeFont {
		return f.cidGlyph(code)
	}
	// A symbolic font is addressed through its own character map first;
	// every other kind goes by name, and both fall back on the same last
	// resorts.
	if f.symbolic {
		if gid, ok := f.byChar(code); ok {
			return gid
		}
	}
	if name, ok := f.names[code]; ok {
		if r, ok := runeOfGlyphName(name); ok {
			if gid, ok := f.program.GlyphIndex(r); ok && gid != 0 {
				return gid
			}
		}
	}
	if gid, ok := f.byChar(code); ok {
		return gid
	}
	// Nothing said which glyph it is; a font whose codes are its own glyph
	// numbers is the last thing left to try, and a number past the end is
	// refused by the outline lookup rather than here.
	return opentype.GlyphIndex(code)
}

// byChar looks a code up as a character, and then in the range the
// specification reserves for a symbolic font's own glyphs.
func (f *pdfFont) byChar(code int) (opentype.GlyphIndex, bool) {
	for _, r := range []rune{rune(code), rune(0xF000 + code)} {
		if gid, ok := f.program.GlyphIndex(r); ok && gid != 0 {
			return gid, true
		}
	}
	return 0, false
}

// cidGlyph maps a character identifier to a glyph.
func (f *pdfFont) cidGlyph(cid int) opentype.GlyphIndex {
	if f.cidToGID == nil {
		return opentype.GlyphIndex(cid)
	}
	i := cid * 2
	if i+1 >= len(f.cidToGID) {
		return 0
	}
	return opentype.GlyphIndex(f.cidToGID[i])<<8 | opentype.GlyphIndex(f.cidToGID[i+1])
}

// loadFont reads a font dictionary into everything needed to draw with it.
func (r *renderer) loadFont(dict reader.Dict) *pdfFont {
	sub, _ := reader.ToName(resolve(r.doc, dict.Get("Subtype")))
	switch sub {
	case "Type0":
		return r.loadComposite(dict)
	case "Type3":
		return r.loadType3(dict)
	}
	return r.loadSimple(dict)
}

// loadSimple reads a font addressed one byte at a time.
func (r *renderer) loadSimple(dict reader.Dict) *pdfFont {
	f := &pdfFont{kind: simpleFont, widths: map[int]float64{}, defaultWidth: 0.5}
	descriptor, _ := r.doc.GetDict(dict, "FontDescriptor")
	f.symbolic = symbolicFlag(r.doc, descriptor)
	f.names = r.simpleNames(dict, f.symbolic)
	r.readSimpleWidths(f, dict)
	r.attachProgram(f, descriptor)
	return f
}

// symbolicFlag reads whether a font says it is addressed through its own
// character map rather than through an encoding of names.
func symbolicFlag(d *reader.Document, descriptor reader.Dict) bool {
	flags, ok := reader.ToInt(resolve(d, descriptor.Get("Flags")))
	if !ok {
		return false
	}
	const symbolic, nonSymbolic = 1 << 2, 1 << 5
	return flags&symbolic != 0 && flags&nonSymbolic == 0
}

// simpleNames works out what each code of a simple font is called.
func (r *renderer) simpleNames(dict reader.Dict, symbolic bool) map[int]string {
	base := standardEncoding
	if !symbolic {
		// A font that is not symbolic and says nothing is read as the standard
		// encoding; naming one is common and changes the base.
		base = standardEncoding
	}
	out := map[int]string{}
	enc := resolve(r.doc, dict.Get("Encoding"))
	if name, ok := reader.ToName(enc); ok {
		base = namedEncoding(name, base)
		fillNames(out, base)
		return out
	}
	encDict, ok := reader.ToDict(enc)
	if !ok {
		fillNames(out, base)
		return out
	}
	if name, ok := reader.ToName(resolve(r.doc, encDict.Get("BaseEncoding"))); ok {
		base = namedEncoding(name, base)
	}
	fillNames(out, base)
	r.applyDifferences(out, encDict)
	return out
}

// namedEncoding picks the table a name stands for.
func namedEncoding(name reader.Name, fallback [256]string) [256]string {
	switch name {
	case "WinAnsiEncoding":
		return winAnsiEncoding
	case "StandardEncoding", "MacRomanEncoding", "MacExpertEncoding":
		// Mac Roman differs from the standard encoding above code 127, where
		// almost nothing in a Latin document lives; using the standard table
		// is closer than using none.
		return standardEncoding
	}
	return fallback
}

// fillNames copies a table into the map a font is looked up through.
func fillNames(out map[int]string, table [256]string) {
	for code, name := range table {
		if name != "" {
			out[code] = name
		}
	}
}

// applyDifferences reads the list that renames individual codes.
func (r *renderer) applyDifferences(out map[int]string, encDict reader.Dict) {
	arr, ok := reader.ToArray(resolve(r.doc, encDict.Get("Differences")))
	if !ok {
		return
	}
	code := 0
	for _, e := range arr {
		e = resolve(r.doc, e)
		if n, ok := reader.ToInt(e); ok {
			code = int(n)
			continue
		}
		if name, ok := reader.ToName(e); ok {
			out[code] = string(name)
			code++
		}
	}
}

// readSimpleWidths reads the widths a simple font gives for its codes.
func (r *renderer) readSimpleWidths(f *pdfFont, dict reader.Dict) {
	first, ok := reader.ToInt(resolve(r.doc, dict.Get("FirstChar")))
	if !ok {
		return
	}
	arr, ok := reader.ToArray(resolve(r.doc, dict.Get("Widths")))
	if !ok {
		return
	}
	for i, e := range arr {
		v, ok := reader.ToFloat(resolve(r.doc, e))
		if !ok {
			continue
		}
		f.widths[int(first)+i] = v / 1000
	}
}

// attachProgram parses whichever font file a descriptor carries.
func (r *renderer) attachProgram(f *pdfFont, descriptor reader.Dict) {
	for _, key := range []reader.Name{"FontFile2", "FontFile3", "FontFile"} {
		stream, ok := reader.ToStream(resolve(r.doc, descriptor.Get(key)))
		if !ok {
			continue
		}
		data, img, err := r.doc.DecodeStream(stream)
		if err != nil || img != "" {
			continue
		}
		program, err := opentype.Parse(data)
		if err != nil {
			// A format this parser does not read leaves the font without
			// outlines; its widths still move the pen, so what follows the
			// text stays where it belongs.
			continue
		}
		f.program = program
		f.perEm = float64(program.UnitsPerEm())
		f.face = program.NewFace(program.UnitsPerEm())
		return
	}
}

// loadComposite reads a font addressed by character identifier.
func (r *renderer) loadComposite(dict reader.Dict) *pdfFont {
	f := &pdfFont{kind: compositeFont, widths: map[int]float64{}, defaultWidth: 1}
	arr, ok := reader.ToArray(resolve(r.doc, dict.Get("DescendantFonts")))
	if !ok || len(arr) == 0 {
		return f
	}
	kid, ok := reader.ToDict(resolve(r.doc, arr[0]))
	if !ok {
		return f
	}
	if v, ok := reader.ToFloat(resolve(r.doc, kid.Get("DW"))); ok {
		f.defaultWidth = v / 1000
	}
	r.readCIDWidths(f, kid)
	descriptor, _ := r.doc.GetDict(kid, "FontDescriptor")
	r.attachProgram(f, descriptor)
	if stream, ok := reader.ToStream(resolve(r.doc, kid.Get("CIDToGIDMap"))); ok {
		if data, img, err := r.doc.DecodeStream(stream); err == nil && img == "" {
			f.cidToGID = data
		}
	}
	return f
}

// readCIDWidths reads the /W array, which names widths one identifier at a
// time or a run at a time.
func (r *renderer) readCIDWidths(f *pdfFont, kid reader.Dict) {
	arr, ok := reader.ToArray(resolve(r.doc, kid.Get("W")))
	if !ok {
		return
	}
	for i := 0; i < len(arr); {
		first, ok := reader.ToInt(resolve(r.doc, arr[i]))
		if !ok || i+1 >= len(arr) {
			return
		}
		next := resolve(r.doc, arr[i+1])
		if list, ok := reader.ToArray(next); ok {
			for k, e := range list {
				if v, ok := reader.ToFloat(resolve(r.doc, e)); ok {
					f.widths[int(first)+k] = v / 1000
				}
			}
			i += 2
			continue
		}
		last, ok := reader.ToInt(next)
		if !ok || i+2 >= len(arr) {
			return
		}
		v, ok := reader.ToFloat(resolve(r.doc, arr[i+2]))
		if ok && last >= first && last-first < 1<<16 {
			for c := first; c <= last; c++ {
				f.widths[int(c)] = v / 1000
			}
		}
		i += 3
	}
}

// loadType3 reads a font whose glyphs are little content streams.
func (r *renderer) loadType3(dict reader.Dict) *pdfFont {
	f := &pdfFont{
		kind:       type3Font,
		widths:     map[int]float64{},
		names:      map[int]string{},
		fontMatrix: geometry.Matrix{Xx: 0.001, Yy: 0.001},
	}
	if arr, ok := reader.ToArray(resolve(r.doc, dict.Get("FontMatrix"))); ok && len(arr) == 6 {
		n := make([]float64, 6)
		good := true
		for i := range n {
			v, ok := reader.ToFloat(resolve(r.doc, arr[i]))
			if !ok {
				good = false
				break
			}
			n[i] = v
		}
		if good {
			f.fontMatrix = matrix(n)
		}
	}
	f.charProcs, _ = r.doc.GetDict(dict, "CharProcs")
	f.t3Resources, _ = r.doc.GetDict(dict, "Resources")
	if encDict, ok := reader.ToDict(resolve(r.doc, dict.Get("Encoding"))); ok {
		r.applyDifferences(f.names, encDict)
	}
	// A Type 3 font's widths are in glyph space, so they go through the font
	// matrix to become text space.
	if first, ok := reader.ToInt(resolve(r.doc, dict.Get("FirstChar"))); ok {
		if arr, ok := reader.ToArray(resolve(r.doc, dict.Get("Widths"))); ok {
			for i, e := range arr {
				if v, ok := reader.ToFloat(resolve(r.doc, e)); ok {
					f.widths[int(first)+i] = v * f.fontMatrix.Xx
				}
			}
		}
	}
	return f
}
