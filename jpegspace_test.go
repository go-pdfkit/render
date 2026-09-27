// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

// greyJPEGPage draws one flat grey JPEG of the given level, in the given
// colour space, over a whole 8 by 8 page.
func greyJPEGPage(t *testing.T, level uint8, space func(w *reader.Writer) reader.Object, extra reader.Dict) *reader.Document {
	t.Helper()
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range src.Pix {
		src.Pix[i] = level
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(8), "Height": reader.Integer(8),
		"ColorSpace": space(w), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode"),
	}
	for k, v := range extra {
		dict[k] = v
	}
	img := w.Add(&reader.Stream{Dict: dict, Raw: buf.Bytes()})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, 8, 8),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 8 0 0 8 0 0 cm /I Do Q")})})
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
	return d
}

// TestASeparationJPEGIsInkAndNotGrey is the defect this was written for.
//
// A Separation's sample is an amount of INK. A tint of nothing is no ink,
// which is paper: white. Read as a level of grey, nothing is black -- so the
// picture comes out as its own negative. Three French tax forms carry a
// PANTONE 293 U logo drawn that way, and a DVLA form's DeviceN "Black" was
// 255 levels from what poppler extracts on every pixel of it.
func TestASeparationJPEGIsInkAndNotGrey(t *testing.T) {
	d := greyJPEGPage(t, 0, func(w *reader.Writer) reader.Object {
		// No tint transform, so the fallback stands: a tint of one is full
		// ink and a tint of nothing is none.
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray")}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Sample 0 is a tint of nothing, which is no ink at all.
	if !isWhite(pic, 4, 4) {
		t.Errorf("a JPEG carrying no ink drew %s, want paper", pixel(pic, 4, 4))
	}
}

// TestASeparationJPEGWithFullInkIsDark is the other end of the same rule, so
// that a change making everything white would not pass.
func TestASeparationJPEGWithFullInkIsDark(t *testing.T) {
	d := greyJPEGPage(t, 255, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray")}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(pic, 4, 4) {
		t.Errorf("a JPEG carrying full ink drew %s, want ink", pixel(pic, 4, 4))
	}
}

// TestADecodeArrayStillAppliesToASeparationJPEG: the samples reach the space
// through the same reading `samples` gives them, so /Decode is not lost on the
// way. A tint written [1 0] means the file stored its ink the other way up.
func TestADecodeArrayStillAppliesToASeparationJPEG(t *testing.T) {
	d := greyJPEGPage(t, 0, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceGray")}
	}, reader.Dict{"Decode": nums(1, 0)})
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Sample 0 through [1 0] is a tint of one: full ink.
	if !isBlack(pic, 4, 4) {
		t.Errorf("a turned-over tint drew %s, want ink", pixel(pic, 4, 4))
	}
}

// TestAnIndexedJPEGReadsItsPalette: an indexed image's samples are row
// numbers, and the colour is whatever the table holds. Every entry here is the
// same blue, so a decoder that hands back the sample as a level of grey cannot
// pass by accident.
func TestAnIndexedJPEGReadsItsPalette(t *testing.T) {
	table := make([]byte, 256*3)
	for i := 0; i < 256; i++ {
		table[i*3], table[i*3+1], table[i*3+2] = 0, 0, 255
	}
	d := greyJPEGPage(t, 128, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Indexed"), reader.Name("DeviceRGB"),
			reader.Integer(255), w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: table})}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := pic.At(4, 4)
	r, g, b, _ := c.RGBA()
	if r>>8 > 8 || g>>8 > 8 || b>>8 < 240 {
		t.Errorf("an indexed JPEG drew %s, want the palette's blue", pixel(pic, 4, 4))
	}
}

// TestAGreyJPEGInAGreySpaceStillDrawsAsGrey holds the three spellings of "this
// is a level of grey" to the answer they had before.
//
// It does NOT test that the switch excludes them: putting DeviceGray through
// the space machinery gives the same pixels, which was checked by making the
// switch accept it and watching this pass. For a grey device space the two
// routes agree by construction, so the switch is about saying what the code
// means and what it costs, not about a different answer. What it guards is the
// regression: a change that put every JPEG through a space would break these.
func TestAGreyJPEGInAGreySpaceStillDrawsAsGrey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		space func(w *reader.Writer) reader.Object
	}{
		{"DeviceGray", func(w *reader.Writer) reader.Object { return reader.Name("DeviceGray") }},
		{"CalGray", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Name("CalGray"), reader.Dict{}}
		}},
		{"ICCBased N=1", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Name("ICCBased"), w.Add(&reader.Stream{
				Dict: reader.Dict{"N": reader.Integer(1)}, Raw: []byte{}})}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := greyJPEGPage(t, 0, tc.space, nil)
			pic, err := Page(d, 1, Options{Scale: 1})
			if err != nil {
				t.Fatal(err)
			}
			if !isBlack(pic, 4, 4) {
				t.Errorf("a black grey JPEG drew %s", pixel(pic, 4, 4))
			}
		})
	}
}

// TestADeviceNOfManyTintsOverAGreyJPEGIsLeftAlone pins the limit rather than
// hiding it.
//
// A DeviceN naming four tints wants four numbers per pixel, and a grey JPEG
// carries one. Nothing here can invent the other three: feeding the one sample
// to all four, or to the first, would be a guess with a colour attached. The
// picture is drawn as the codec produced it, which is what happened before
// this existed, and the file gets no worse for having been looked at.
func TestADeviceNOfManyTintsOverAGreyJPEGIsLeftAlone(t *testing.T) {
	d := greyJPEGPage(t, 0, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("DeviceN"),
			reader.Array{reader.Name("C"), reader.Name("M"), reader.Name("Y"), reader.Name("K")},
			reader.Name("DeviceCMYK")}
	}, nil)
	pic, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isBlack(pic, 4, 4) {
		t.Errorf("drew %s, want the codec's own output", pixel(pic, 4, 4))
	}
}

// gradientJPEGPage is greyJPEGPage with every one of the 256 levels present, and
// large enough that converting per pixel costs measurably more than converting
// 256 times. A gradient rather than a flat fill so that a memo cannot be confused
// with a shortcut for an image of one colour.
func gradientJPEGPage(t *testing.T, side int, space func(w *reader.Writer) reader.Object) *reader.Document {
	t.Helper()
	src := image.NewGray(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			src.Pix[y*src.Stride+x] = uint8((x + y) % 256)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	img := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(side), "Height": reader.Integer(side),
		"ColorSpace": space(w), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode"),
	}, Raw: buf.Bytes()})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, float64(side), float64(side)),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte(fmt.Sprintf("q %d 0 0 %d 0 0 cm /I Do Q", side, side))})})
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
	return d
}

// TestAOneComponentJPEGIsConvertedOncePerLevelRatherThanOncePerPixel.
//
// An eight-bit sample has 256 possible values, so a calibrated space needs to be
// asked 256 times and not once per pixel. Without that memo a DVLA form carrying
// one 2480x3508 ICCBased grey scan -- 8.7 million pixels -- spent 72% of its time
// in a math.Pow per sample, 535 ms against poppler's 159. With it the page takes
// 66 ms, and the pixels are identical because 256 answers are ALL the answers.
//
// The bound is a RATIO against a witness the conversion does not touch: the same
// picture in DeviceGray, whose samples pass straight through. A duration would pin
// this machine's speed and a conversion count would need a hook in the space.
//
// THE GAMMA IS EXPLICIT AND THAT IS THE POINT. This test was first written with
// `[/CalGray <<>>]` and it PASSED with the memo removed: a CalGray with no Gamma
// converts for free, so it is the one calibrated space that cannot witness this
// defect. Measured at 512 by 512, with the memo and without:
//
//	DeviceGray             4.35 ms    4.51 ms   (the witness)
//	CalGray, no Gamma      4.40 ms    4.44 ms   <- sees nothing
//	CalGray, Gamma 2.2     4.88 ms   47.59 ms   <- 9.8x
//	Separation, exp N=2.4  4.73 ms   19.94 ms   <- 4.2x
//
// So the bound is 3x: the memoised page sits at 1.12x of the witness and the
// unmemoised one at 10.9x, which leaves room on both sides without pinning either.
func TestAOneComponentJPEGIsConvertedOncePerLevelRatherThanOncePerPixel(t *testing.T) {
	const side = 512 // 262 144 pixels against 256 levels
	device := func(*reader.Writer) reader.Object { return reader.Name("DeviceGray") }
	calibrated := func(*reader.Writer) reader.Object {
		return reader.Array{reader.Name("CalGray"), reader.Dict{
			"WhitePoint": nums(0.9505, 1, 1.089), "Gamma": reader.Real(2.2)}}
	}
	// Drawn once each first, so neither timing pays for a cold cache the other
	// does not.
	draw(t, gradientJPEGPage(t, side, device), Options{})
	draw(t, gradientJPEGPage(t, side, calibrated), Options{})

	best := func(space func(*reader.Writer) reader.Object) time.Duration {
		d := gradientJPEGPage(t, side, space)
		out := time.Duration(1 << 62)
		for i := 0; i < 3; i++ {
			start := time.Now()
			draw(t, d, Options{})
			if took := time.Since(start); took < out {
				out = took
			}
		}
		return out
	}
	witness := best(device)
	subject := best(calibrated)
	if subject > 3*witness {
		t.Errorf("a calibrated grey JPEG took %v against DeviceGray's %v (%.1fx); "+
			"the colour space is being asked per pixel rather than per level",
			subject, witness, float64(subject)/float64(witness))
	}
}

// rgbJPEGPage draws one RGB JPEG over a whole page of the same size, in the given
// colour space. The picture is whatever fill writes into it.
func rgbJPEGPage(t *testing.T, side int, space func(w *reader.Writer) reader.Object, fill func(x, y int) (uint8, uint8, uint8)) *reader.Document {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			r, g, b := fill(x, y)
			i := y*src.Stride + x*4
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = r, g, b, 255
		}
	}
	var buf bytes.Buffer
	// Lossless is not on offer, so the assertions below read the DECODED samples
	// back rather than the ones written here.
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	img := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(side), "Height": reader.Integer(side),
		"ColorSpace": space(w), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode"),
	}, Raw: buf.Bytes()})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox":  nums(0, 0, float64(side), float64(side)),
		"Resources": reader.Dict{"XObject": reader.Dict{"I": img}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte(fmt.Sprintf("q %d 0 0 %d 0 0 cm /I Do Q", side, side))})})
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
	return d
}

// calRGB is a calibrated space whose conversion is not free, so it can witness
// whether the samples went through it.
func calRGB(*reader.Writer) reader.Object {
	return reader.Array{reader.Name("CalRGB"), reader.Dict{
		"WhitePoint": nums(0.9505, 1, 1.089),
		"Gamma":      nums(2.2, 2.2, 2.2)}}
}

// TestTheTripleCacheDoesNotChangeAPicture.
//
// A three-component picture is cached on its sample triple, and the failure that
// would matter is a cache answering for the wrong colour: the picture would be
// wrong everywhere that colour appears, consistently, which no smoke test notices.
//
// The reference is the SAME code path with a cache that cannot be crowded: a one
// by one picture of one colour has one triple in it, so what it draws is the
// uncached truth for that triple. Every pixel of the large picture is compared
// against the one-pixel answer for the triple that pixel actually holds, so the
// JPEG's own lossiness cannot make this test lie.
func TestTheTripleCacheDoesNotChangeAPicture(t *testing.T) {
	const side = 64
	// Distinct colours, spread so they do not land in one run of slots.
	fill := func(x, y int) (uint8, uint8, uint8) {
		v := y*side + x
		return uint8(v * 7), uint8(v * 13), uint8(v * 29)
	}
	big := draw(t, rgbJPEGPage(t, side, calRGB, fill), Options{})

	// What the decoder actually produced, so the reference is asked about the
	// triples that reached the cache rather than the ones written into the file.
	plain := draw(t, rgbJPEGPage(t, side, func(*reader.Writer) reader.Object {
		return reader.Name("DeviceRGB")
	}, fill), Options{})

	// A one-pixel reference has to be ASKED what it decoded to. JPEG is lossy at
	// every quality, so a file written with the triple 58,6,40 in it can come back
	// as 56,5,39 -- and comparing against that would report a cache defect that is
	// really the codec. The first version of this test did exactly that.
	deviceRGB := func(*reader.Writer) reader.Object { return reader.Name("DeviceRGB") }
	seen := map[[3]uint8][3]uint8{}
	compared, skipped := 0, 0
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			s := plain.At(x, y)
			key := [3]uint8{s.R, s.G, s.B}
			want, known := seen[key]
			if !known {
				flat := func(int, int) (uint8, uint8, uint8) { return s.R, s.G, s.B }
				// What the one-pixel file actually holds once decoded.
				got := draw(t, rgbJPEGPage(t, 1, deviceRGB, flat), Options{}).At(0, 0)
				if got.R != s.R || got.G != s.G || got.B != s.B {
					seen[key] = [3]uint8{0, 0, 0}
					skipped++
					continue
				}
				// One triple, one slot, no crowding: the uncached answer.
				c := draw(t, rgbJPEGPage(t, 1, calRGB, flat), Options{}).At(0, 0)
				want = [3]uint8{c.R, c.G, c.B}
				seen[key] = want
			} else if want == [3]uint8{0, 0, 0} {
				skipped++
				continue
			}
			got := big.At(x, y)
			if got.R != want[0] || got.G != want[1] || got.B != want[2] {
				t.Fatalf("(%d,%d) sample %v: cached %d,%d,%d but uncached %v",
					x, y, key, got.R, got.G, got.B, want)
			}
			compared++
		}
	}
	if compared < 500 {
		t.Errorf("only %d pixels could be compared (%d skipped because the one-pixel "+
			"reference did not decode to the triple asked of it); the test is not "+
			"exercising the cache", compared, skipped)
	}
}

// TestAThreeComponentJPEGIsCachedOnItsSampleTriple pins the speed the cache is for.
//
// Sixteen million triples are not a memo, but a picture is not sixteen million
// colours: the corpus's slowest remaining page, a 2313x2956 ICCBased RGB scan, has
// 6 837 228 pixels and 208 801 distinct triples -- 3.05%, so 97 conversions in 100
// are repeats. Cached, that page goes from 790 ms to 202 ms against poppler's 193.
//
// The bound is a ratio against DeviceRGB, whose samples pass straight through, and
// the gamma on the calibrated space is explicit for the reason the one-component
// test records: a calibrated space with no gamma converts for free and cannot
// witness anything.
func TestAThreeComponentJPEGIsCachedOnItsSampleTriple(t *testing.T) {
	const side = 320
	// Few distinct colours relative to the pixel count, which is what a scan looks
	// like: 320x320 is 102 400 pixels over at most 4 096 triples here.
	//
	// The variation is in the LOW bits and red barely moves, which is also what a
	// scan looks like -- a page of one ink under a lamp. A cache that indexed on
	// the key's low bits alone would pile these into a few hundred slots and miss
	// almost every time, so this fill is what makes the hash a tested decision
	// rather than a comment.
	fill := func(x, y int) (uint8, uint8, uint8) {
		return uint8(200 + x%4), uint8(180 + y%8), uint8(x%16 + y%16*16)
	}
	deviceRGB := func(*reader.Writer) reader.Object { return reader.Name("DeviceRGB") }
	draw(t, rgbJPEGPage(t, side, deviceRGB, fill), Options{})
	draw(t, rgbJPEGPage(t, side, calRGB, fill), Options{})

	best := func(space func(*reader.Writer) reader.Object) time.Duration {
		d := rgbJPEGPage(t, side, space, fill)
		out := time.Duration(1 << 62)
		for i := 0; i < 3; i++ {
			start := time.Now()
			draw(t, d, Options{})
			if took := time.Since(start); took < out {
				out = took
			}
		}
		return out
	}
	witness := best(deviceRGB)
	subject := best(calRGB)
	if subject > 3*witness {
		t.Errorf("a calibrated RGB JPEG took %v against DeviceRGB's %v (%.1fx); "+
			"the colour space is being asked per pixel rather than per distinct triple",
			subject, witness, float64(subject)/float64(witness))
	}
}
