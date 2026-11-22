package render

import (
	"errors"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/go-pdfkit/reader"
)

// SVGOptions say how a page is written as SVG.
type SVGOptions struct {
	// MaxBytes refuses a document that would come out larger than this, with
	// [ErrSVGTooLarge], and writes nothing. Zero means 64 MiB. A page a file
	// made up can be asked for any number of marks, and each is a few dozen
	// bytes of text: this is what bounds the output.
	MaxBytes int
	// RasterScale is how many pixels a point gets in the pictures embedded for
	// what SVG cannot say as a shape: a tiling or shading pattern, a soft mask,
	// a transparency group under a mask. Zero means two. The page's own
	// coordinates are unaffected; only these pictures change in sharpness (and
	// in size).
	RasterScale float64
	// MaxDuration is how long the page may be interpreted for, as for
	// [Options.MaxDuration]. When it runs out the page is written as far as it
	// got and [ErrTimedOut] is returned.
	MaxDuration time.Duration
}

// ErrSVGTooLarge says the page would have come out past SVGOptions.MaxBytes.
// Nothing was written.
var ErrSVGTooLarge = errors.New("render: the SVG would be larger than its limit")

const (
	defaultSVGMaxBytes    = 64 << 20
	defaultSVGRasterScale = 2
	// svgMaxPixels bounds the pictures embedded for what shapes cannot say,
	// which are drawn on papers as large as the page at the raster scale.
	svgMaxPixels = 16 << 20
)

// PageSVG writes the i'th page of a document, counting from one, as SVG.
//
// Paths, text, clips, images and transparency groups become SVG elements;
// everything that has no equivalent in SVG 1.1 — tiling and shading patterns,
// shadings, soft masks — is drawn by the pixel renderer and embedded as an
// image where it falls. Text is written as outlines, so the output depends on no
// font. The document holds only numbers, generated ids and base64 data: nothing
// the PDF says is copied into it as text.
func PageSVG(d *reader.Document, i int, w io.Writer, opt SVGOptions) error {
	scale := opt.RasterScale
	if !(scale > 0) || math.IsInf(scale, 0) {
		scale = defaultSVGRasterScale
	}
	limit := opt.MaxBytes
	if limit <= 0 {
		limit = defaultSVGMaxBytes
	}
	var dev *svgDevice
	_, _, err := renderPageWith(d, i, Options{Scale: scale, MaxPixels: svgMaxPixels,
		MaxDuration: opt.MaxDuration}, func(r *renderer) device {
		dev = newSVGDevice(r, scale, limit)
		return dev
	})
	if dev == nil {
		return err
	}
	if dev.full() {
		return ErrSVGTooLarge
	}
	// Past this point the only error is a timeout, and the page is written as
	// far as it got.
	if _, werr := w.Write(dev.document()); werr != nil {
		return werr
	}
	return err
}

// num writes a coordinate: three decimals, no trailing zeros, and never NaN or
// an infinity, which SVG cannot read.
func num(v float64) string {
	if math.IsNaN(v) {
		v = 0
	}
	v = math.Max(-1e7, math.Min(1e7, v))
	b := strconv.AppendFloat(make([]byte, 0, 16), v, 'f', 3, 64)
	for b[len(b)-1] == '0' {
		b = b[:len(b)-1]
	}
	if b[len(b)-1] == '.' {
		b = b[:len(b)-1]
	}
	if len(b) == 2 && b[0] == '-' && b[1] == '0' {
		return "0"
	}
	return string(b)
}

// matrixNum writes an entry of a transform, which has to keep its significant
// digits rather than its decimals: a glyph's matrix is a few thousandths.
func matrixNum(v float64) string {
	if math.IsNaN(v) {
		v = 0
	}
	v = math.Max(-1e9, math.Min(1e9, v))
	return strconv.FormatFloat(v, 'g', 7, 64)
}
