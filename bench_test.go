package render

import (
	"fmt"
	"strings"
	"testing"
)

// Benchmarks, because the question "would SIMD make this faster" cannot be
// asked of a package with no baseline.
//
// This package had 43 test files and not one benchmark, and so do `reader`,
// `ops`, `extract` and `opentype`. The only measured corner of the whole PDF
// stack is `go-gfx/gfx`, which has 13 — and the only generated SIMD anywhere
// in it is `gfx/resample`, for amd64, arm64 and s390x.
//
// ⛔ These say nothing on their own. A number from a machine under load is a
// number about the load: take them with `-count=6` on a quiet machine and
// compare with benchstat, never one run against one run.
//
// What each is for, in terms of where work could move:
//
//	Paths   gfx/vector's scanline rasteriser. No SIMD today.
//	Text    opentype outline loading, then the same rasteriser.
//	Images  unpacking samples, then gfx/resample — the one path that IS
//	        already SIMD, so it is the control: a change there should show
//	        here and nowhere else.
//	DPI     the same page at three scales, which says whether cost follows
//	        pixels or follows operators.

// paths draws n filled and stroked rectangles, which is the shape a chart or a
// table is made of.
func pathsContent(n int) string {
	var b strings.Builder
	for i := range n {
		x := float64(i%40) * 14
		y := float64(i/40) * 14
		fmt.Fprintf(&b, "%.1f %.1f 12 12 re f\n", x, y)
		fmt.Fprintf(&b, "0.5 w %.1f %.1f 12 12 re S\n", x+1, y+1)
	}
	return b.String()
}

func BenchmarkPagePaths(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprint(n, "-rects"), func(b *testing.B) {
			d := onePage(b, [4]float64{0, 0, 595, 842}, pathsContent(n), nil)
			b.ResetTimer()
			for b.Loop() {
				if _, err := Page(d, 1, Options{DPI: 150}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The same page at three scales. If the cost follows the pixel count rather
// than the operator count, the work is in the rasteriser and not in the
// content stream — which is the first thing worth knowing before moving any
// of it into assembly.
func BenchmarkPageByScale(b *testing.B) {
	d := onePage(b, [4]float64{0, 0, 595, 842}, pathsContent(400), nil)
	for _, dpi := range []float64{72, 150, 300} {
		b.Run(fmt.Sprintf("%.0fdpi", dpi), func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				if _, err := Page(d, 1, Options{DPI: dpi}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// An empty page at the same size, so the fixed cost of opening, laying out and
// allocating the raster is visible beside the drawing in every other
// benchmark. A measurement with no floor under it reports the floor as if it
// were the work.
func BenchmarkPageEmpty(b *testing.B) {
	d := onePage(b, [4]float64{0, 0, 595, 842}, "", nil)
	b.ResetTimer()
	for b.Loop() {
		if _, err := Page(d, 1, Options{DPI: 150}); err != nil {
			b.Fatal(err)
		}
	}
}

// Many small fills with a colour change between each, which is the shape of a
// shaded plot and the case where per-operator cost dominates per-pixel cost.
func BenchmarkPageColourChanges(b *testing.B) {
	var sb strings.Builder
	for i := range 2000 {
		g := float64(i%100) / 100
		fmt.Fprintf(&sb, "%.2f %.2f %.2f rg %.1f %.1f 6 6 re f\n",
			g, 1-g, g/2, float64(i%80)*7, float64(i/80)*7)
	}
	d := onePage(b, [4]float64{0, 0, 595, 842}, sb.String(), nil)
	b.ResetTimer()
	for b.Loop() {
		if _, err := Page(d, 1, Options{DPI: 150}); err != nil {
			b.Fatal(err)
		}
	}
}
