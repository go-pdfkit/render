// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// TestAGlyphOutlineIsReadOnceAndKept.
//
// An outline is in font units, so it does not depend on the size a glyph is drawn
// at or on the transform that places it: the glyph index is the whole key. Without
// the cache the face was asked again for every occurrence -- AcroFormsBasicFields.pdf
// asked 145 543 times for 95 distinct glyphs, which was 638 MB of the 1.58 GB that
// page allocated.
//
// The test reads the BACKING ARRAY's address: GlyphOutline builds a fresh slice on
// every call, so the same array coming back twice can only mean the second call did
// not reach it. A length or a deep comparison would pass whether or not anything was
// cached.
func TestAGlyphOutlineIsReadOnceAndKept(t *testing.T) {
	_, f := fontOf(t, reader.Dict{"BaseFont": reader.Name("Helvetica")})
	if f == nil {
		t.Fatal("no font")
	}
	const code = 'H'
	first, ok := f.glyph(code)
	if !ok || len(first) == 0 {
		t.Fatalf("no outline for %q", code)
	}
	second, ok := f.glyph(code)
	if !ok {
		t.Fatal("the second read failed")
	}
	if &first[0] != &second[0] {
		t.Error("the face was asked twice for the same glyph")
	}
	if len(f.outlines) != 1 {
		t.Errorf("the cache holds %d outlines after one glyph", len(f.outlines))
	}
	// A different glyph is a different entry rather than the first one again.
	other, ok := f.glyph('e')
	if ok && len(other) > 0 && len(first) > 0 && &other[0] == &first[0] {
		t.Error("a different glyph came back as the first one")
	}
}

// TestAGlyphTheFontDoesNotCarryIsNotAskedForTwice.
//
// A page shows a missing glyph as often as it shows any other, and a failed read
// costs what a successful one costs. The refusal is remembered separately from the
// outlines, because a nil outline and no outline are not the same answer.
func TestAGlyphTheFontDoesNotCarryIsNotAskedForTwice(t *testing.T) {
	_, f := fontOf(t, reader.Dict{"BaseFont": reader.Name("Helvetica")})
	if f == nil {
		t.Fatal("no font")
	}
	// A code far past anything the stand-in carries.
	const absent = 0xFFFE
	if _, ok := f.glyph(absent); ok {
		t.Skip("the face carries this code after all, so there is nothing to refuse")
	}
	if !f.missing[f.glyphIndex(absent)] {
		t.Error("the refusal was not remembered")
	}
	if _, ok := f.glyph(absent); ok {
		t.Error("the second read gave a different answer")
	}
	if _, cached := f.outlines[f.glyphIndex(absent)]; cached {
		t.Error("a glyph with no outline was put in the outline cache")
	}
}

// TestACachedOutlineDrawsTheSamePicture, because the cheapest way to break this is
// to hand back the wrong glyph's outline.
func TestACachedOutlineDrawsTheSamePicture(t *testing.T) {
	// Two words sharing letters, so most glyphs are drawn more than once.
	d := pageNamingAFont(t, "BT /F 20 Tf 5 20 Td (banana) Tj ET",
		reader.Dict{"BaseFont": reader.Name("Helvetica")})
	first := draw(t, d, Options{})
	second := draw(t, d, Options{})
	b := first.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p, q := first.At(x, y), second.At(x, y)
			if p.R != q.R || p.G != q.G || p.B != q.B {
				t.Fatalf("(%d,%d): %v then %v", x, y, p, q)
			}
		}
	}
	if inked(first) == 0 {
		t.Error("nothing was drawn, so this compares two blank pages")
	}
}
