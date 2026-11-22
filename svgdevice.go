package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"strconv"
	"time"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
)

// An svgDevice writes the marks a page makes as SVG instead of painting them.
//
// Everything is written in device pixels at the raster scale, under a viewBox
// of the page's pixel size and a width and height in points, so a shape and a
// picture embedded for a pattern share one coordinate system.
//
// What the text holds is only what this file generates: numbers, ids made from
// counters, colours as hex, and base64. Nothing a PDF says is ever copied in.
type svgDevice struct {
	r     *renderer
	scale float64
	limit int

	body, defs []byte
	over       bool
	nextID     int
	glyphs     map[[32]byte]string
	images     map[[32]byte]string

	// white and black are the two papers an embedded picture is drawn on, so
	// that its transparency can be recovered from the difference.
	white, black *raster.Image
}

func newSVGDevice(r *renderer, scale float64, limit int) *svgDevice {
	return &svgDevice{r: r, scale: scale, limit: limit,
		glyphs: map[[32]byte]string{}, images: map[[32]byte]string{}}
}

// full says the output has passed its limit, and stops the page being
// interpreted any further: there is nothing left to write it to.
func (d *svgDevice) full() bool {
	if !d.over && len(d.body)+len(d.defs) > d.limit {
		d.over = true
		d.r.deadline = time.Unix(1, 0)
	}
	return d.over
}

// document is the finished SVG.
func (d *svgDevice) document() []byte {
	w, h := d.r.img.W, d.r.img.H
	out := make([]byte, 0, len(d.body)+len(d.defs)+256)
	out = append(out, `<svg xmlns="http://www.w3.org/2000/svg" width="`...)
	out = append(out, num(float64(w)/d.scale)...)
	out = append(out, `" height="`...)
	out = append(out, num(float64(h)/d.scale)...)
	out = append(out, `" viewBox="0 0 `...)
	out = strconv.AppendInt(out, int64(w), 10)
	out = append(out, ' ')
	out = strconv.AppendInt(out, int64(h), 10)
	out = append(out, `">`...)
	if len(d.defs) > 0 {
		out = append(out, `<defs>`...)
		out = append(out, d.defs...)
		out = append(out, `</defs>`...)
	}
	out = append(out, `<rect width="`...)
	out = strconv.AppendInt(out, int64(w), 10)
	out = append(out, `" height="`...)
	out = strconv.AppendInt(out, int64(h), 10)
	out = append(out, `" fill="#ffffff"/>`...)
	out = append(out, d.body...)
	return append(out, `</svg>`...)
}

func (d *svgDevice) id(prefix string) string {
	d.nextID++
	return prefix + strconv.Itoa(d.nextID)
}

func (d *svgDevice) put(parts ...string) {
	for _, p := range parts {
		d.body = append(d.body, p...)
	}
}

// open and shut wrap what is written in the clip in force.
func (d *svgDevice) open(g *gstate) bool {
	if g.svgClip == "" {
		return false
	}
	d.put(`<g clip-path="url(#`, g.svgClip, `)">`)
	return true
}

func (d *svgDevice) shut(opened bool) {
	if opened {
		d.put(`</g>`)
	}
}

func hexColour(c color.RGBA) string {
	const h = "0123456789abcdef"
	return string([]byte{'#', h[c.R>>4], h[c.R&15], h[c.G>>4], h[c.G&15], h[c.B>>4], h[c.B&15]})
}

func unit(a float64) float64 {
	if !(a > 0) {
		return 0
	}
	return math.Min(a, 1)
}

// opacity is an attribute for an alpha below one, and nothing for one.
func opacity(name string, a float64) string {
	if a = unit(a); a >= 1 {
		return ""
	}
	return ` ` + name + `="` + num(a) + `"`
}

// pathData writes a path mapped by m as SVG path data.
func pathData(p *userPath, m geometry.Matrix) []byte {
	var b []byte
	started := false
	pt := func(q geometry.Point) {
		q = m.TransformPoint(q)
		b = append(b, ' ')
		b = append(b, num(q.X)...)
		b = append(b, ' ')
		b = append(b, num(q.Y)...)
	}
	for _, s := range p.segs {
		if s.op != segMove && !started {
			continue
		}
		switch s.op {
		case segMove:
			started = true
			b = append(b, 'M')
			pt(s.p[0])
		case segLine:
			b = append(b, 'L')
			pt(s.p[0])
		case segQuad:
			b = append(b, 'Q')
			pt(s.p[0])
			pt(s.p[1])
		case segCubic:
			b = append(b, 'C')
			pt(s.p[0])
			pt(s.p[1])
			pt(s.p[2])
		case segClose:
			b = append(b, 'Z')
		}
	}
	return b
}

func transformAttr(m geometry.Matrix) string {
	return ` transform="matrix(` + matrixNum(m.Xx) + ` ` + matrixNum(m.Xy) + ` ` + matrixNum(m.Yx) + ` ` +
		matrixNum(m.Yy) + ` ` + matrixNum(m.X0) + ` ` + matrixNum(m.Y0) + `)"`
}

func rule(r vector.FillRule) string {
	if r == vector.EvenOdd {
		return ` fill-rule="evenodd"`
	}
	return ""
}

// glyphDef writes a glyph's outline once, in its own units, and returns its id.
func (d *svgDevice) glyphDef(p *userPath) string {
	h := sha256.New()
	var buf [8]byte
	for _, s := range p.segs {
		h.Write([]byte{byte(s.op)})
		for _, q := range s.p {
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(q.X))
			h.Write(buf[:])
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(q.Y))
			h.Write(buf[:])
		}
	}
	var key [32]byte
	h.Sum(key[:0])
	if id, ok := d.glyphs[key]; ok {
		return id
	}
	id := d.id("g")
	d.glyphs[key] = id
	d.defs = append(d.defs, `<path id="`+id+`" d="`...)
	d.defs = append(d.defs, pathData(p, geometry.Identity())...)
	d.defs = append(d.defs, `"/>`...)
	return id
}

// extent is the part of the page a path mapped by m reaches, grown by pad.
func (d *svgDevice) extent(p *userPath, m geometry.Matrix, pad float64) image.Rectangle {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, s := range p.segs {
		n := 1
		switch s.op {
		case segQuad:
			n = 2
		case segCubic:
			n = 3
		case segClose:
			n = 0
		}
		for _, q := range s.p[:n] {
			q = m.TransformPoint(q)
			minX, minY = math.Min(minX, q.X), math.Min(minY, q.Y)
			maxX, maxY = math.Max(maxX, q.X), math.Max(maxY, q.Y)
		}
	}
	if minX > maxX {
		return image.Rectangle{} // no point at all
	}
	return d.region(minX-pad, minY-pad, maxX+pad, maxY+pad)
}

// region turns a box in pixels into whole pixels inside the page. The numbers
// are bounded as floats first: a file can write any coordinate.
func (d *svgDevice) region(x0, y0, x1, y1 float64) image.Rectangle {
	w, h := float64(d.r.img.W), float64(d.r.img.H)
	c := func(v, hi float64) int {
		switch {
		case !(v > 0): // nothing, below the page, or NaN
			return 0
		case v >= hi:
			return int(hi)
		}
		return int(v)
	}
	return image.Rect(c(math.Floor(x0), w), c(math.Floor(y0), h), c(math.Ceil(x1), w), c(math.Ceil(y1), h))
}

func (d *svgDevice) page() image.Rectangle { return image.Rect(0, 0, d.r.img.W, d.r.img.H) }

// needsPixels says a mark cannot be written as a shape: it is painted with a
// pattern, or through a soft mask, both of which only the rasteriser knows.
func needsPixels(g *gstate, pattern *pattern) bool { return pattern != nil || g.softMask != nil }

func (d *svgDevice) fillPath(g *gstate, p *userPath, m geometry.Matrix, fr vector.FillRule, resources reader.Dict) {
	if d.full() {
		return
	}
	if needsPixels(g, g.fillPattern) {
		d.fallback(g, d.extent(p, m, 1), func(rd rasterDevice, gg *gstate) { rd.fillPath(gg, p, m, fr, resources) })
		return
	}
	if unit(g.fillAlpha) <= 0 || len(p.segs) == 0 {
		return
	}
	o := d.open(g)
	if p.glyph {
		d.put(`<use href="#`, d.glyphDef(p), `"`, transformAttr(m), ` fill="`, hexColour(g.fill), `"`,
			opacity("fill-opacity", g.fillAlpha), rule(fr), `/>`)
	} else {
		d.put(`<path d="`, string(pathData(p, m)), `" fill="`, hexColour(g.fill), `"`,
			opacity("fill-opacity", g.fillAlpha), rule(fr), `/>`)
	}
	d.shut(o)
}

func (d *svgDevice) strokePath(g *gstate, p *userPath, m geometry.Matrix, resources reader.Dict) {
	if d.full() {
		return
	}
	style := d.r.strokeStyle(g)
	if needsPixels(g, g.strokePattern) {
		pad := style.Width * math.Max(style.MiterLimit, 1)
		d.fallback(g, d.extent(p, m, pad+1), func(rd rasterDevice, gg *gstate) { rd.strokePath(gg, p, m, resources) })
		return
	}
	if unit(g.strokeAlpha) <= 0 || len(p.segs) == 0 {
		return
	}
	o := d.open(g)
	d.put(`<path d="`, string(pathData(p, m)), `" fill="none" stroke="`, hexColour(g.stroke), `"`,
		opacity("stroke-opacity", g.strokeAlpha), ` stroke-width="`, num(style.Width), `"`)
	switch style.Cap {
	case vector.RoundCap:
		d.put(` stroke-linecap="round"`)
	case vector.SquareCap:
		d.put(` stroke-linecap="square"`)
	}
	switch style.Join {
	case vector.RoundJoin:
		d.put(` stroke-linejoin="round"`)
	case vector.BevelJoin:
		d.put(` stroke-linejoin="bevel"`)
	}
	if ml := style.MiterLimit; ml >= 1 && ml != 4 {
		d.put(` stroke-miterlimit="`, num(ml), `"`)
	}
	d.dash(style)
	d.put(`/>`)
	d.shut(o)
}

// dash writes the dash pattern when it is one SVG accepts: no negative length,
// and some length that is not nothing.
func (d *svgDevice) dash(style vector.StrokeStyle) {
	sum := 0.0
	for _, v := range style.Dash {
		if v < 0 || v != v {
			return
		}
		sum += v
	}
	if sum <= 0 {
		return
	}
	d.put(` stroke-dasharray="`)
	for i, v := range style.Dash {
		if i > 0 {
			d.put(` `)
		}
		d.put(num(v))
	}
	d.put(`"`)
	if style.DashPhase != 0 {
		d.put(` stroke-dashoffset="`, num(style.DashPhase), `"`)
	}
}

func (d *svgDevice) embolden(g *gstate, p *userPath, m geometry.Matrix, width float64, resources reader.Dict) {
	if d.full() {
		return
	}
	if needsPixels(g, g.fillPattern) {
		d.fallback(g, d.extent(p, m, width+1), func(rd rasterDevice, gg *gstate) { rd.embolden(gg, p, m, width, resources) })
		return
	}
	if unit(g.fillAlpha) <= 0 || len(p.segs) == 0 {
		return
	}
	o := d.open(g)
	d.put(`<path d="`, string(pathData(p, m)), `" fill="none" stroke="`, hexColour(g.fill), `"`,
		opacity("stroke-opacity", g.fillAlpha), ` stroke-width="`, num(width),
		`" stroke-linecap="round" stroke-linejoin="round"/>`)
	d.shut(o)
}

func (d *svgDevice) clipPath(g *gstate, p *userPath, m geometry.Matrix, fr vector.FillRule, rect *[4]float64) {
	if d.full() {
		return
	}
	id := d.id("c")
	d.defs = append(d.defs, `<clipPath id="`+id+`"`...)
	if g.svgClip != "" {
		d.defs = append(d.defs, ` clip-path="url(#`+g.svgClip+`)"`...)
	}
	d.defs = append(d.defs, `><path d="`...)
	d.defs = append(d.defs, pathData(p, m)...)
	d.defs = append(d.defs, `"`...)
	if fr == vector.EvenOdd {
		d.defs = append(d.defs, ` clip-rule="evenodd"`...)
	}
	d.defs = append(d.defs, `/></clipPath>`...)
	g.svgClip = id
}

func (d *svgDevice) image(g *gstate, s *sampled) {
	if d.full() {
		return
	}
	if g.softMask != nil {
		d.fallback(g, unitSquareBounds(g.ctm, d.r.img.W, d.r.img.H), func(rd rasterDevice, gg *gstate) { rd.image(gg, s) })
		return
	}
	if unit(g.fillAlpha) <= 0 || s.w <= 0 || s.h <= 0 || g.ctm.Determinant() == 0 {
		return
	}
	id := d.imageDef(s)
	// The unit square counts up from its bottom and a picture counts down from
	// its top row.
	flip := geometry.Matrix{Xx: 1 / float64(s.w), Yy: -1 / float64(s.h), Y0: 1}
	o := d.open(g)
	d.put(`<use href="#`, id, `"`, transformAttr(g.ctm.Mul(flip)), opacity("opacity", g.fillAlpha), `/>`)
	d.shut(o)
}

// imageDef embeds a picture once and returns its id. A picture with no
// see-through pixel is written as a JPEG, and one with any as a PNG: JPEG keeps
// a scan a tenth of the size and cannot hold alpha, and a PNG cannot be made
// smaller without losing what was drawn.
func (d *svgDevice) imageDef(s *sampled) string {
	h := sha256.New()
	var dims [16]byte
	binary.LittleEndian.PutUint64(dims[:], uint64(s.w))
	binary.LittleEndian.PutUint64(dims[8:], uint64(s.h))
	h.Write(dims[:])
	h.Write(s.pix)
	for _, c := range s.pal {
		h.Write([]byte{c.R, c.G, c.B, c.A})
	}
	var key [32]byte
	h.Sum(key[:0])
	if id, ok := d.images[key]; ok {
		return id
	}
	img := image.NewNRGBA(image.Rect(0, 0, s.w, s.h))
	opaque := true
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			c := s.at(x, y)
			if c.A != 255 {
				opaque = false
			}
			img.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A})
		}
	}
	id := d.id("i")
	d.images[key] = id
	d.defs = append(d.defs, `<image id="`+id+`" width="`...)
	d.defs = strconv.AppendInt(d.defs, int64(s.w), 10)
	d.defs = append(d.defs, `" height="`...)
	d.defs = strconv.AppendInt(d.defs, int64(s.h), 10)
	d.defs = append(d.defs, `" href="`...)
	d.defs = append(d.defs, dataURI(img, opaque)...)
	d.defs = append(d.defs, `"/>`...)
	return id
}

// dataURI encodes a picture. An encoder failing on an in-memory picture cannot
// happen, so a failure would write an empty picture rather than a document with
// a hole in it.
func dataURI(img *image.NRGBA, opaque bool) string {
	var buf bytes.Buffer
	mime := "image/png"
	if opaque {
		mime = "image/jpeg"
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	} else {
		_ = png.Encode(&buf, img)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func (d *svgDevice) shade(g *gstate, sh *shading) {
	if d.full() {
		return
	}
	d.fallback(g, d.page(), func(rd rasterDevice, gg *gstate) { rd.shade(gg, sh) })
}

// An svgGroup is what a group keeps while its content runs. A group under a
// soft mask is drawn by the rasteriser on a paper of its own; one that is only
// faded is a <g> with an opacity.
type svgGroup struct {
	masked     bool
	region     image.Rectangle
	paper      *raster.Image
	painted    []float32
	wasImg     *raster.Image
	wasPainted []float32
	wasDev     device
}

func (d *svgDevice) beginGroup(g *gstate, ctm geometry.Matrix, box *[4]float64) (any, bool) {
	if d.full() {
		return nil, false
	}
	r := d.r
	if g.softMask == nil {
		d.put(`<g`, opacity("opacity", g.fillAlpha), `>`)
		return &svgGroup{}, true
	}
	region := d.page()
	if box != nil {
		region = boxBounds(ctm, *box, r.img.W, r.img.H)
	}
	if region.Empty() {
		return nil, false
	}
	grp := &svgGroup{masked: true, region: region, paper: raster.New(r.img.W, r.img.H),
		painted: make([]float32, r.img.W*r.img.H), wasImg: r.img, wasPainted: r.painted, wasDev: r.dev}
	fill(grp.paper, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	r.img, r.painted, r.dev = grp.paper, grp.painted, rasterDevice{r}
	return grp, true
}

func (d *svgDevice) endGroup(g *gstate, token any) {
	grp := token.(*svgGroup)
	if !grp.masked {
		d.put(`</g>`)
		return
	}
	r := d.r
	r.img, r.painted, r.dev = grp.wasImg, grp.wasPainted, grp.wasDev
	out := image.NewNRGBA(image.Rect(0, 0, r.img.W, r.img.H))
	for y := grp.region.Min.Y; y < grp.region.Max.Y; y++ {
		for x := grp.region.Min.X; x < grp.region.Max.X; x++ {
			i := y*r.img.W + x
			cover := float64(grp.painted[i])
			if cover <= 0 {
				continue
			}
			a := cover * unit(g.fillAlpha) * maskLevel(g.softMask[i])
			// The paper was white, so what shows is colour*cover + white*(1-cover).
			un := func(v uint8) uint8 {
				return uint8(math.Round(math.Max(0, math.Min(255, (float64(v)-255*(1-cover))/cover))))
			}
			out.SetNRGBA(x, y, color.NRGBA{R: un(grp.paper.Pix[i*4]), G: un(grp.paper.Pix[i*4+1]),
				B: un(grp.paper.Pix[i*4+2]), A: uint8(math.Round(255 * math.Min(a, 1)))})
		}
	}
	d.emit(g, out, grp.region)
}

// fallback draws one mark with the rasteriser, twice, on a white paper and on a
// black one, and embeds the result as an image: what a pixel was over white and
// over black says both its colour and how much of it there is.
func (d *svgDevice) fallback(g *gstate, region image.Rectangle, op func(rd rasterDevice, gg *gstate)) {
	region = region.Intersect(d.page())
	if region.Empty() {
		return
	}
	r := d.r
	if d.white == nil {
		d.white, d.black = raster.New(r.img.W, r.img.H), raster.New(r.img.W, r.img.H)
		fill(d.white, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		fill(d.black, color.RGBA{A: 255})
	}
	wasImg, wasDev := r.img, r.dev
	defer func() { r.img, r.dev = wasImg, wasDev }()
	r.dev = rasterDevice{r}
	for _, paper := range []*raster.Image{d.white, d.black} {
		gg := *g
		r.img = paper
		op(rasterDevice{r}, &gg)
	}
	out := image.NewNRGBA(image.Rect(0, 0, r.img.W, r.img.H))
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			i := (y*r.img.W + x) * 4
			w, b := d.white.Pix[i:i+3], d.black.Pix[i:i+3]
			// Over black a pixel is colour*alpha, over white it is that plus
			// 255*(1-alpha).
			a := 1 - float64(int(w[0])-int(b[0])+int(w[1])-int(b[1])+int(w[2])-int(b[2]))/(3*255)
			if a <= 0.002 {
				continue
			}
			un := func(v uint8) uint8 { return uint8(math.Round(math.Max(0, math.Min(255, float64(v)/a)))) }
			out.SetNRGBA(x, y, color.NRGBA{R: un(b[0]), G: un(b[1]), B: un(b[2]), A: uint8(math.Round(255 * math.Min(a, 1)))})
		}
	}
	d.restore(region)
	d.emit(g, out, region)
}

// restore puts the two papers back to white and black where a mark was drawn.
func (d *svgDevice) restore(region image.Rectangle) {
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			i := (y*d.white.W + x) * 4
			d.white.Pix[i], d.white.Pix[i+1], d.white.Pix[i+2], d.white.Pix[i+3] = 255, 255, 255, 255
			d.black.Pix[i], d.black.Pix[i+1], d.black.Pix[i+2], d.black.Pix[i+3] = 0, 0, 0, 255
		}
	}
}

// emit writes the part of a picture that is not see-through as an <image>, at
// the pixels it came from.
func (d *svgDevice) emit(g *gstate, img *image.NRGBA, region image.Rectangle) {
	box := image.Rectangle{}
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if box.Empty() {
		return
	}
	o := d.open(g)
	d.put(`<image x="`, strconv.Itoa(box.Min.X), `" y="`, strconv.Itoa(box.Min.Y), `" width="`,
		strconv.Itoa(box.Dx()), `" height="`, strconv.Itoa(box.Dy()), `" href="`,
		dataURI(img.SubImage(box).(*image.NRGBA), false), `"/>`)
	d.shut(o)
}
