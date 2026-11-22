package render

import (
	"bytes"
	"image"
	imgdraw "image/draw"
	"testing"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// The SVG of a page that is only shapes is drawn by an independent renderer,
// oksvg, and laid beside what this package's own rasteriser draws.
//
// oksvg is a small renderer and says so: it has no <image>, no clipPath, no
// miter limit, and it never reads fill-rule (measured: the even-odd page it
// draws differs from the raster by 24 levels on average, because it fills
// non-zero; the even-odd output is held by the allowlist and scenario tests
// instead). The pages
// compared here are the ones it can draw, and the tolerance below is what two
// different antialiasers disagree by on the edges of shapes — not a number
// chosen to pass: it is measured, and the worst page is reported.
const (
	// meanTolerance is the mean absolute difference per channel over the page.
	meanTolerance = 1.0
	// farFraction is the largest share of pixels allowed to differ by more than
	// 96 levels in some channel: edge pixels only.
	farFraction = 0.01
)

func oksvgRender(t *testing.T, doc []byte, w, h int) *raster.Image {
	t.Helper()
	icon, err := oksvg.ReadIconStream(bytes.NewReader(doc), oksvg.IgnoreErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	imgdraw.Draw(rgba, rgba.Bounds(), image.White, image.Point{}, imgdraw.Src)
	icon.SetTarget(0, 0, float64(w), float64(h))
	scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
	icon.Draw(rasterx.NewDasher(w, h, scanner), 1)
	out := raster.New(w, h)
	copy(out.Pix, rgba.Pix)
	return out
}

// compare returns the mean difference per channel and the share of far pixels.
func compare(a, b *raster.Image) (mean, far float64) {
	var sum float64
	farPixels := 0
	for i := 0; i < len(a.Pix); i += 4 {
		worst := 0
		for c := 0; c < 3; c++ {
			d := int(a.Pix[i+c]) - int(b.Pix[i+c])
			if d < 0 {
				d = -d
			}
			sum += float64(d)
			worst = max(worst, d)
		}
		if worst > 96 {
			farPixels++
		}
	}
	return sum / float64(len(a.Pix)/4*3), float64(farPixels) / float64(len(a.Pix)/4)
}

func TestVectorPagesAgreeWithAnIndependentRenderer(t *testing.T) {
	cases := []struct {
		name string
		page func(t *testing.T) *reader.Document
	}{
		{"filled shapes", func(t *testing.T) *reader.Document {
			return onePage(t, [4]float64{0, 0, 100, 100},
				"1 0 0 rg 10 10 40 40 re f 0 0.6 0 rg 50 50 m 90 50 l 70 90 l h f 0 0 1 rg 20 60 m 20 95 60 95 60 60 c f", nil)
		}},
		{"strokes", func(t *testing.T) *reader.Document {
			return onePage(t, [4]float64{0, 0, 100, 100},
				"0 g 6 w 10 20 m 90 20 l S 1 J 1 j 10 w 20 50 m 50 80 l 80 50 l S 0 J 0 j 3 w [6 4] 0 d 10 90 m 90 90 l S", nil)
		}},
		{"a page of text", func(t *testing.T) *reader.Document {
			return pageWithFont(t, "BT /F 24 Tf 5 30 Td (HAHA) Tj ET BT /F 16 Tf 5 10 Td (Hi) Tj ET", nil)
		}},
		{"a rotated page", func(t *testing.T) *reader.Document {
			return onePage(t, [4]float64{0, 0, 100, 60}, "0 0 1 rg 10 10 30 20 re f 1 0 0 RG 4 w 50 10 40 40 re S",
				reader.Dict{"Rotate": reader.Integer(90)})
		}},
		{"faded marks", func(t *testing.T) *reader.Document {
			return shadedPage(t, "0 g 10 10 50 50 re f /GS0 gs 1 0 0 rg 30 30 60 60 re f",
				func(w *reader.Writer) reader.Dict {
					return reader.Dict{"ExtGState": reader.Dict{"GS0": w.Add(reader.Dict{"ca": nums(0.5)[0]})}}
				})
		}},
	}
	worstMean, worstFar := 0.0, 0.0
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.page(t)
			doc := svgOf(t, d, SVGOptions{RasterScale: 2})
			want, err := Page(d, 1, Options{Scale: 2})
			if err != nil {
				t.Fatal(err)
			}
			got := oksvgRender(t, doc, want.W, want.H)
			mean, far := compare(got, want)
			t.Logf("%s: mean difference %.2f levels, %.2f%% of pixels far", c.name, mean, far*100)
			worstMean, worstFar = max(worstMean, mean), max(worstFar, far)
			if mean > meanTolerance || far > farFraction {
				t.Errorf("the SVG drawn by oksvg differs from the raster: mean %.2f (limit %.1f), far %.3f (limit %.2f)",
					mean, meanTolerance, far, farFraction)
			}
		})
	}
	t.Logf("worst page: mean %.2f levels, %.2f%% far", worstMean, worstFar*100)
}
