package render

import (
	"math"

	gfxcolor "github.com/go-gfx/gfx/color"
	"github.com/go-pdfkit/reader"
	"image/color"
)

// A space is a colour space: how many numbers name a colour in it, and how to
// turn those numbers into something that can be put on a screen.
type space struct {
	name       reader.Name
	components int
	// convert turns the operands of a colour operator into a colour.
	convert func(v []float64) color.RGBA
	// lookup, for an indexed space, is the table the index reads from.
	lookup []byte
	// base, for an indexed space, is what the table holds.
	base *space
	// pattern reports a space whose colours come from a pattern rather than
	// from numbers, which nothing here can draw yet.
	pattern bool
}

// The device spaces, which every file may use without saying anything first.
var (
	deviceGray   = &space{name: "DeviceGray", components: 1, convert: grayToRGBA}
	deviceRGB    = &space{name: "DeviceRGB", components: 3, convert: rgbToRGBA}
	deviceCMYK   = &space{name: "DeviceCMYK", components: 4, convert: cmykToRGBA}
	patternSpace = &space{name: "Pattern", components: 1, convert: grayToRGBA, pattern: true}
)

// initial is the colour a space starts at: black, in whatever way it says it.
func (s *space) initial() color.RGBA {
	v := make([]float64, s.components)
	if s.name == "DeviceCMYK" {
		v[3] = 1
	}
	return s.convert(v)
}

// grayToRGBA reads one number as a level of grey.
func grayToRGBA(v []float64) color.RGBA {
	g := channel(v, 0)
	return color.RGBA{R: g, G: g, B: g, A: 255}
}

// rgbToRGBA reads three numbers as red, green and blue.
func rgbToRGBA(v []float64) color.RGBA {
	return color.RGBA{R: channel(v, 0), G: channel(v, 1), B: channel(v, 2), A: 255}
}

// cmykToRGBA reads four numbers as cyan, magenta, yellow and black.
func cmykToRGBA(v []float64) color.RGBA {
	r, g, b := gfxcolor.CMYKToSRGB(gfxcolor.CMYK{
		C: clamp01(at(v, 0)), M: clamp01(at(v, 1)),
		Y: clamp01(at(v, 2)), K: clamp01(at(v, 3)),
	})
	return color.RGBA{R: byteOf(r), G: byteOf(g), B: byteOf(b), A: 255}
}

// at reads one operand, or zero when there are not that many.
func at(v []float64, i int) float64 {
	if i >= len(v) {
		return 0
	}
	return v[i]
}

// channel reads one operand as a byte of colour.
func channel(v []float64, i int) uint8 { return byteOf(clamp01(at(v, i))) }

// clamp01 holds a number between nothing and everything.
func clamp01(v float64) float64 { return math.Min(1, math.Max(0, v)) }

// byteOf turns a fraction into one of the 256 levels a screen has.
func byteOf(v float64) uint8 { return uint8(math.Round(clamp01(v) * 255)) }

// colourSpace works out what a name or an array in a resource dictionary
// means. An unknown space reads as grey, which is the least surprising thing
// to draw in.
func (r *renderer) colourSpace(o reader.Object, resources reader.Dict, depth int) *space {
	if depth > 8 {
		return deviceGray
	}
	o = resolve(r.doc, o)
	if name, ok := reader.ToName(o); ok {
		if s := deviceSpace(name); s != nil {
			return s
		}
		// A name that is not a device space names one in the resources.
		spaces, ok := r.doc.GetDict(resources, "ColorSpace")
		if !ok {
			return deviceGray
		}
		entry := spaces.Get(name)
		if entry.Kind() == reader.KindNull {
			return deviceGray
		}
		return r.colourSpace(entry, resources, depth+1)
	}
	arr, ok := reader.ToArray(o)
	if !ok || len(arr) == 0 {
		return deviceGray
	}
	family, _ := reader.ToName(resolve(r.doc, arr[0]))
	return r.colourSpaceArray(family, arr, resources, depth)
}

// colourSpaceArray reads the array forms, which is where every space that
// needs saying more than its name lives.
func (r *renderer) colourSpaceArray(family reader.Name, arr reader.Array, resources reader.Dict, depth int) *space {
	switch family {
	case "ICCBased":
		// The profile is not interpreted; how many components it has is what
		// decides how its numbers are read, which is what the specification
		// says to fall back on.
		if len(arr) > 1 {
			if s, ok := reader.ToStream(resolve(r.doc, arr[1])); ok {
				if n, ok := reader.ToInt(resolve(r.doc, s.Dict.Get("N"))); ok {
					return byComponents(int(n))
				}
			}
		}
		return deviceRGB
	case "CalRGB":
		return deviceRGB
	case "CalGray":
		return deviceGray
	case "Lab":
		return labSpace()
	case "Indexed":
		return r.indexedSpace(arr, resources, depth)
	case "Separation", "DeviceN":
		return r.separationSpace(family, arr, resources, depth)
	case "Pattern":
		return patternSpace
	case "DeviceGray", "DeviceRGB", "DeviceCMYK":
		return deviceSpace(family)
	}
	return deviceGray
}

// deviceSpace reports the space a device name means, or nil.
func deviceSpace(name reader.Name) *space {
	switch name {
	case "DeviceGray", "G", "CalGray":
		return deviceGray
	case "DeviceRGB", "RGB", "CalRGB":
		return deviceRGB
	case "DeviceCMYK", "CMYK":
		return deviceCMYK
	case "Pattern":
		return patternSpace
	}
	return nil
}

// byComponents picks the device space that reads that many numbers.
func byComponents(n int) *space {
	switch n {
	case 1:
		return deviceGray
	case 4:
		return deviceCMYK
	}
	return deviceRGB
}

// labSpace reads three numbers as lightness and two opponent axes. Only the
// lightness is used, which is a grey of the right weight rather than the right
// colour — enough not to lose the mark, and honest about what it is.
func labSpace() *space {
	return &space{name: "Lab", components: 3, convert: func(v []float64) color.RGBA {
		g := byteOf(at(v, 0) / 100)
		return color.RGBA{R: g, G: g, B: g, A: 255}
	}}
}

// indexedSpace reads one number as a row of a table of colours.
func (r *renderer) indexedSpace(arr reader.Array, resources reader.Dict, depth int) *space {
	if len(arr) < 4 {
		return deviceGray
	}
	base := r.colourSpace(arr[1], resources, depth+1)
	table := r.lookupTable(arr[3])
	s := &space{name: "Indexed", components: 1, lookup: table, base: base}
	s.convert = func(v []float64) color.RGBA {
		i := int(math.Round(at(v, 0)))
		if i < 0 {
			i = 0
		}
		start := i * base.components
		if start+base.components > len(table) {
			return color.RGBA{A: 255}
		}
		comps := make([]float64, base.components)
		for k := range comps {
			comps[k] = float64(table[start+k]) / 255
		}
		return base.convert(comps)
	}
	return s
}

// lookupTable reads an indexed space's table, which a file may write as a
// string or as a stream.
func (r *renderer) lookupTable(o reader.Object) []byte {
	resolved := resolve(r.doc, o)
	if s, ok := reader.ToString(resolved); ok {
		return s
	}
	if stream, ok := reader.ToStream(resolved); ok {
		data, img, err := r.doc.DecodeStream(stream)
		if err == nil && img == "" {
			return data
		}
	}
	return nil
}

// separationSpace reads one or more tints. The tint transform that says what
// those tints look like is a function, which this wave does not evaluate; a
// tint of one is taken to be full ink and a tint of nothing to be none, which
// is right for the common case of a single spot colour standing in for black.
func (r *renderer) separationSpace(family reader.Name, arr reader.Array, resources reader.Dict, depth int) *space {
	n := 1
	if family == "DeviceN" {
		if names, ok := reader.ToArray(resolve(r.doc, arr[1])); ok {
			n = len(names)
		}
	}
	if n < 1 {
		n = 1
	}
	return &space{name: family, components: n, convert: func(v []float64) color.RGBA {
		tint := 0.0
		for i := 0; i < n; i++ {
			tint = math.Max(tint, clamp01(at(v, i)))
		}
		g := byteOf(1 - tint)
		return color.RGBA{R: g, G: g, B: g, A: 255}
	}}
}
