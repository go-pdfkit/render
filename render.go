// Package render turns a page of a PDF into pixels.
//
// It reads with github.com/go-pdfkit/reader and rasterises with
// github.com/go-gfx/gfx. Nothing outside those and the standard library is
// used, so it builds for js/wasm and for every architecture the fleet targets
// — which is what lets a PDF be shown in a browser tab with nothing on the far
// end.
package render

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
	"image/color"
)

// Options say how a page is to be drawn.
type Options struct {
	// Scale is device pixels per point. Zero means one, which is seventy-two
	// to the inch; [Options.DPI] is the same thing said the other way.
	Scale float64
	// DPI sets the scale from dots per inch. It is ignored when Scale is set.
	DPI float64
	// Background is painted before anything else. The zero value is opaque
	// white, which is what paper is.
	Background *color.RGBA
	// MaxPixels refuses a page that would come out larger than this, so a
	// media box a file made up cannot be asked to fill memory. Zero means
	// forty million, which is a little over A4 at 600 dots to the inch.
	MaxPixels int
	// NoAnnotations leaves out what the page carries beside its content: the
	// notes, the highlights, the stamps, and the fields of a form. They are
	// drawn by default, because a filled-in form drawn without them is a form
	// with nothing in it.
	NoAnnotations bool
	// MaxDuration is how long the page may be drawn for. Zero means as long as
	// it takes, which is what this did before there was anywhere to say
	// otherwise.
	//
	// Some pages take a very long time. Of 59 432 corpus pages drawn at half
	// as much again as their own size, 1 131 were still going after twenty
	// seconds, and one figure took two hundred and seventy-three. A caller
	// drawing somebody else's file — a browser tab, a queue of them, a server
	// — cannot afford to wait for the worst of those and has, until now, had
	// no way to say so: MaxPixels bounds what comes out, and nothing bounded
	// the work of making it.
	//
	// When the time runs out the page stops being drawn and comes back as far
	// as it got, with ErrTimedOut. That is deliberately not a blank: half a
	// page is worth more than nothing to somebody scrolling, and the error
	// says plainly that it is half.
	MaxDuration time.Duration
}

// ErrTimedOut says a page was still being drawn when its time ran out. The
// image returned with it holds what had been drawn by then.
var ErrTimedOut = errors.New("render: the page was still being drawn when its time ran out")

// defaultMaxPixels is a little more than A4 at 600 dots to the inch.
const defaultMaxPixels = 40 << 20

// scale works out how many device pixels there are to a point.
func (o Options) scale() float64 {
	switch {
	case o.Scale > 0:
		return o.Scale
	case o.DPI > 0:
		return o.DPI / 72
	}
	return 1
}

// background is the colour the paper starts as.
func (o Options) background() color.RGBA {
	if o.Background != nil {
		return *o.Background
	}
	return color.RGBA{R: 255, G: 255, B: 255, A: 255}
}

// maxPixels is how large a page may be.
func (o Options) maxPixels() int {
	if o.MaxPixels > 0 {
		return o.MaxPixels
	}
	return defaultMaxPixels
}

// Page draws the i'th page of a document, counting from one.
func Page(d *reader.Document, i int, opt Options) (*raster.Image, error) {
	page, err := d.Page(i)
	if err != nil {
		return nil, err
	}
	box := pageBox(d, page)
	rotation := pageRotation(d, page)
	s := opt.scale()
	// A box with no extent is not a box, and a scale is never zero, so
	// rounding up always leaves at least one pixel.
	w := int(math.Ceil((box[2] - box[0]) * s))
	h := int(math.Ceil((box[3] - box[1]) * s))
	if rotation == 90 || rotation == 270 {
		w, h = h, w
	}
	if w*h > opt.maxPixels() {
		return nil, fmt.Errorf("render: page %d would be %d by %d pixels, past the limit of %d", i, w, h, opt.maxPixels())
	}
	img := raster.New(w, h)
	fill(img, opt.background())

	content, err := d.PageContent(i)
	if err != nil {
		return nil, err
	}
	resources, _ := d.GetDict(page, "Resources")
	r := &renderer{doc: d, img: img, fonts: map[int]*pdfFont{}, softMasks: map[softMaskKey][]uint8{}}
	if opt.MaxDuration > 0 {
		r.deadline = time.Now().Add(opt.MaxDuration)
	}
	start := r.initialState(box, rotation, s)
	r.base = start.ctm
	r.run(content, resources, start)
	if !opt.NoAnnotations {
		r.drawAnnotations(page, start)
	}
	if r.ranOut {
		return img, ErrTimedOut
	}
	return img, nil
}

// fill paints the whole image one colour.
func fill(img *raster.Image, c color.RGBA) {
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			img.Set(x, y, c)
		}
	}
}

// initialState is the graphics state a page starts in: the transform that maps
// its own coordinates onto the image, and the defaults every page begins with.
func (r *renderer) initialState(box [4]float64, rotation int, s float64) gstate {
	// PDF counts upwards from the bottom left of the box; an image counts
	// downwards from its top left.
	flip := geometry.Matrix{
		Xx: s, Yx: 0, X0: -box[0] * s,
		Xy: 0, Yy: -s, Y0: box[3] * s,
	}
	w, h := (box[2]-box[0])*s, (box[3]-box[1])*s
	var turn geometry.Matrix
	switch rotation {
	case 90:
		turn = geometry.Matrix{Xx: 0, Yx: -1, X0: h, Xy: 1, Yy: 0, Y0: 0}
	case 180:
		turn = geometry.Matrix{Xx: -1, Yx: 0, X0: w, Xy: 0, Yy: -1, Y0: h}
	case 270:
		turn = geometry.Matrix{Xx: 0, Yx: 1, X0: 0, Xy: -1, Yy: 0, Y0: w}
	default:
		turn = geometry.Identity()
	}
	return gstate{
		ctm:         turn.Mul(flip),
		fill:        color.RGBA{A: 255},
		stroke:      color.RGBA{A: 255},
		fillSpace:   deviceGray,
		strokeSpace: deviceGray,
		lineWidth:   1,
		miterLimit:  10,
		fillAlpha:   1,
		strokeAlpha: 1,
	}
}

// pageBox is the area of the page that gets drawn: what it says should be
// visible, or the paper it is on.
func pageBox(d *reader.Document, page reader.Dict) [4]float64 {
	for _, key := range []reader.Name{"CropBox", "MediaBox"} {
		if b, ok := rectangle(d, page.Get(key)); ok {
			return b
		}
	}
	return [4]float64{0, 0, 612, 792}
}

// pageRotation reads a page's /Rotate, normalised to a right angle.
func pageRotation(d *reader.Document, page reader.Dict) int {
	n, ok := reader.ToInt(resolve(d, page.Get("Rotate")))
	if !ok || n%90 != 0 {
		return 0
	}
	deg := int(n) % 360
	if deg < 0 {
		deg += 360
	}
	return deg
}

// rectangle reads a PDF rectangle, normalised so the first corner is the lower
// left one — files do write them the other way round.
func rectangle(d *reader.Document, o reader.Object) ([4]float64, bool) {
	var out [4]float64
	arr, ok := reader.ToArray(resolve(d, o))
	if !ok || len(arr) < 4 {
		return out, false
	}
	for i := 0; i < 4; i++ {
		v, ok := reader.ToFloat(resolve(d, arr[i]))
		if !ok {
			return out, false
		}
		out[i] = v
	}
	if out[0] > out[2] {
		out[0], out[2] = out[2], out[0]
	}
	if out[1] > out[3] {
		out[1], out[3] = out[3], out[1]
	}
	if out[0] == out[2] || out[1] == out[3] {
		return out, false
	}
	return out, true
}

// resolve follows an indirect reference. A document that opened cannot fail to
// resolve one, so there is nothing to handle at every use.
func resolve(d *reader.Document, o reader.Object) reader.Object {
	out, _ := d.Resolve(o)
	return out
}
