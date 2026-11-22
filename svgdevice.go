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
	groups     []svgGroup
	over, deep bool
	nextID     int
	glyphs     map[[32]byte]string
	images     map[[32]byte]string

	// clips knows what each clip the device wrote is, and clipIDs finds one
	// already written, so that a page that says the same clip a thousand times
	// is written it once.
	clips   map[string]svgClipInfo
	clipIDs map[string]string

	// white and black are the two papers an embedded picture is drawn on, so
	// that its transparency can be recovered from the difference. scratch is
	// the one buffer the pictures are read back into, and spare the papers a
	// finished group under a mask leaves for the next.
	white, black *raster.Image
	scratch      []byte
	spare        []svgPaper
}

// An svgClipInfo is what a clip written to the document is: a rectangle (in
// pixels) when it is one, so that a rectangle inside a rectangle is written as
// the intersection and not as a chain, and how deep a chain of clips it ends.
type svgClipInfo struct {
	rect           bool
	x0, y0, x1, y1 float64
	depth          int
}

// svgClipDepth is how many clips may be nested in the document, which is as many
// as a reader can follow: past it a page is refused with [ErrSVGTooDeep]. A reader
// follows a chain of clip paths by recursion, and one of twenty thousand
// overflows the stack of one that is in common use.
const svgClipDepth = 64

type svgPaper struct {
	paper   *raster.Image
	painted []float32
}

func newSVGDevice(r *renderer, scale float64, limit int) *svgDevice {
	return &svgDevice{r: r, scale: scale, limit: limit,
		glyphs: map[[32]byte]string{}, images: map[[32]byte]string{},
		clips: map[string]svgClipInfo{}, clipIDs: map[string]string{}}
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

// clipPath narrows the clip of the rasteriser as the pixel renderer would, which
// is what bounds every picture the device embeds, and writes the same clip into
// the document.
func (d *svgDevice) clipPath(g *gstate, p *userPath, m geometry.Matrix, fr vector.FillRule, rect [4]float64, isRect bool) {
	if d.full() {
		return
	}
	rasterDevice{d.r}.clipPath(g, p, m, fr, rect, isRect)
	parent := d.clips[g.svgClip] // the zero clip when there is none
	if isRect && m.Xy == 0 && m.Yx == 0 && (g.svgClip == "" || parent.rect) {
		a := m.TransformPoint(geometry.Point{X: rect[0], Y: rect[1]})
		b := m.TransformPoint(geometry.Point{X: rect[0] + rect[2], Y: rect[1] + rect[3]})
		x0, x1 := math.Min(a.X, b.X), math.Max(a.X, b.X)
		y0, y1 := math.Min(a.Y, b.Y), math.Max(a.Y, b.Y)
		if finite(x0, y0, x1, y1) {
			if g.svgClip != "" {
				x0, y0 = math.Max(x0, parent.x0), math.Max(y0, parent.y0)
				x1, y1 = math.Min(x1, parent.x1), math.Min(y1, parent.y1)
				x1, y1 = math.Max(x0, x1), math.Max(y0, y1)
			}
			data := "M " + num(x0) + " " + num(y0) + "L " + num(x1) + " " + num(y0) + "L " + num(x1) + " " +
				num(y1) + "L " + num(x0) + " " + num(y1) + "Z"
			g.svgClip = d.clipDef("", data, false, svgClipInfo{rect: true, x0: x0, y0: y0, x1: x1, y1: y1, depth: 1})
			return
		}
	}
	depth := parent.depth + 1
	if depth > svgClipDepth {
		d.over, d.deep = true, true
		d.r.deadline = time.Unix(1, 0)
		return
	}
	g.svgClip = d.clipDef(g.svgClip, string(pathData(p, m)), fr == vector.EvenOdd, svgClipInfo{depth: depth})
}

func finite(v ...float64) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// clipDef writes a clip under another, or finds the one already written.
func (d *svgDevice) clipDef(under, data string, evenodd bool, info svgClipInfo) string {
	key := under + "|" + data
	if evenodd {
		key += "|e"
	}
	if id, ok := d.clipIDs[key]; ok {
		return id
	}
	id := d.id("c")
	d.clipIDs[key] = id
	d.clips[id] = info
	d.defs = append(d.defs, `<clipPath id="`+id+`"`...)
	if under != "" {
		d.defs = append(d.defs, ` clip-path="url(#`+under+`)"`...)
	}
	d.defs = append(d.defs, `><path d="`...)
	d.defs = append(d.defs, data...)
	d.defs = append(d.defs, `"`...)
	if evenodd {
		d.defs = append(d.defs, ` clip-rule="evenodd"`...)
	}
	d.defs = append(d.defs, `/></clipPath>`...)
	return id
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
	h.Write([]byte{'s'}) // a picture of the page's own, which is another kind of key
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

// beginGroup keeps what it needs of the group in a stack of its own, never the
// graphics state it was given: that is a frame lent for one run.
func (d *svgDevice) beginGroup(g *gstate, ctm geometry.Matrix, box [4]float64, hasBox bool) bool {
	if d.full() {
		return false
	}
	r := d.r
	if g.softMask == nil {
		d.put(`<g`, opacity("opacity", g.fillAlpha), `>`)
		d.groups = append(d.groups, svgGroup{})
		return true
	}
	region := d.page()
	if hasBox {
		region = boxBounds(ctm, box, r.img.W, r.img.H)
	}
	// Nothing in the group can reach past the clip it starts under, which is why
	// only this much of its paper is read and cleaned.
	region = region.Intersect(d.clipBox(g))
	if region.Empty() {
		return false
	}
	grp := svgGroup{masked: true, region: region, wasImg: r.img, wasPainted: r.painted, wasDev: r.dev}
	if n := len(d.spare); n > 0 {
		grp.paper, grp.painted = d.spare[n-1].paper, d.spare[n-1].painted
		d.spare = d.spare[:n-1]
	} else {
		grp.paper, grp.painted = raster.New(r.img.W, r.img.H), make([]float32, r.img.W*r.img.H)
		fill(grp.paper, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	}
	r.img, r.painted, r.dev = grp.paper, grp.painted, rasterDevice{r}
	d.groups = append(d.groups, grp)
	return true
}

func (d *svgDevice) endGroup(g *gstate) {
	grp := d.groups[len(d.groups)-1]
	d.groups = d.groups[:len(d.groups)-1]
	if !grp.masked {
		d.put(`</g>`)
		return
	}
	r := d.r
	r.img, r.painted, r.dev = grp.wasImg, grp.wasPainted, grp.wasDev
	out := d.canvas(grp.region)
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
	d.emit(out, grp.region)
	// The paper goes back to white and the grid to nothing where the group
	// was, to serve the next.
	for y := grp.region.Min.Y; y < grp.region.Max.Y; y++ {
		for x := grp.region.Min.X; x < grp.region.Max.X; x++ {
			i := y*r.img.W + x
			grp.painted[i] = 0
			grp.paper.Pix[i*4], grp.paper.Pix[i*4+1], grp.paper.Pix[i*4+2], grp.paper.Pix[i*4+3] = 255, 255, 255, 255
		}
	}
	d.spare = append(d.spare, svgPaper{grp.paper, grp.painted})
}

// fallback draws one mark with the rasteriser, twice, on a white paper and on a
// black one, and embeds the result as an image: what a pixel was over white and
// over black says both its colour and how much of it there is.
func (d *svgDevice) fallback(g *gstate, region image.Rectangle, op func(rd rasterDevice, gg *gstate)) {
	// Nothing is drawn past the clip, so nothing past it is read back: a shading
	// under a one-point clip is a one-point picture, not a page of them.
	region = region.Intersect(d.clipBox(g))
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
	out := d.canvas(region)
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
	d.emit(out, region)
}

// clipBox is the part of the page the clip in force lets anything through.
func (d *svgDevice) clipBox(g *gstate) image.Rectangle {
	if g.clip == nil {
		return d.page()
	}
	return image.Rect(g.clip.ox, g.clip.oy, g.clip.ox+g.clip.w, g.clip.oy+g.clip.h).Intersect(d.page())
}

// canvas is a cleared picture over a region of the page, in a buffer kept from
// one picture to the next: a page of a thousand small pictures allocates the
// largest of them once, and not a page each.
func (d *svgDevice) canvas(region image.Rectangle) *image.NRGBA {
	n := region.Dx() * region.Dy() * 4
	if cap(d.scratch) < n {
		d.scratch = make([]byte, n)
	}
	pix := d.scratch[:n]
	clear(pix)
	return &image.NRGBA{Pix: pix, Stride: region.Dx() * 4, Rect: region}
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
//
// The picture was drawn through the clip in force, so it is not clipped again.
// One that is the same as an earlier one, pixel for pixel, is defined once and
// used again where it falls.
func (d *svgDevice) emit(img *image.NRGBA, region image.Rectangle) {
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
	sub := img.SubImage(box).(*image.NRGBA)
	h := sha256.New()
	h.Write([]byte{'e'})
	var dims [16]byte
	binary.LittleEndian.PutUint64(dims[:], uint64(box.Dx()))
	binary.LittleEndian.PutUint64(dims[8:], uint64(box.Dy()))
	h.Write(dims[:])
	for y := 0; y < box.Dy(); y++ {
		h.Write(sub.Pix[y*sub.Stride : y*sub.Stride+box.Dx()*4])
	}
	var key [32]byte
	h.Sum(key[:0])
	id, ok := d.images[key]
	if !ok {
		id = d.id("i")
		d.images[key] = id
		d.defs = append(d.defs, `<image id="`+id+`" width="`...)
		d.defs = strconv.AppendInt(d.defs, int64(box.Dx()), 10)
		d.defs = append(d.defs, `" height="`...)
		d.defs = strconv.AppendInt(d.defs, int64(box.Dy()), 10)
		d.defs = append(d.defs, `" href="`...)
		d.defs = append(d.defs, dataURI(sub, false)...)
		d.defs = append(d.defs, `"/>`...)
	}
	d.put(`<use href="#`, id, `" transform="matrix(1 0 0 1 `, strconv.Itoa(box.Min.X), ` `, strconv.Itoa(box.Min.Y), `)"/>`)
}
