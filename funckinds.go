package render

import (
	"math"

	"github.com/go-pdfkit/reader"
)

// A sampledFn is a grid of numbers with the values in between read off
// by straight lines: a colour ramp written out rather than described.
type sampledFn struct {
	functionBase
	size    []int
	bps     int
	encode  []float64
	decode  []float64
	n       int // outputs per sample
	samples []byte
}

func (f *sampledFn) outputs() int { return f.n }

// readSampled reads a function type 0: the grid, how wide each number in it
// is, and how the inputs and outputs are mapped onto and off it.
func (r *renderer) readSampled(base functionBase, dict reader.Dict, stream *reader.Stream) function {
	if stream == nil || len(base.rng) < 2 {
		return nil
	}
	data, img, err := r.doc.DecodeStream(stream)
	if err != nil || img != "" {
		return nil
	}
	sizes := r.floatArray(dict.Get("Size"))
	if len(sizes) == 0 || len(sizes) != len(base.domain)/2 {
		return nil
	}
	f := &sampledFn{functionBase: base, n: len(base.rng) / 2, samples: data}
	total := 1
	for _, s := range sizes {
		if s < 1 || s > 1<<20 {
			return nil
		}
		f.size = append(f.size, int(s))
		total *= int(s)
		if total > 1<<24 {
			return nil
		}
	}
	bps, ok := reader.ToInt(resolve(r.doc, dict.Get("BitsPerSample")))
	if !ok {
		return nil
	}
	switch bps {
	case 1, 2, 4, 8, 12, 16, 24, 32:
		f.bps = int(bps)
	default:
		return nil
	}
	f.encode = r.floatArray(dict.Get("Encode"))
	if len(f.encode) != 2*len(f.size) {
		f.encode = nil
		for _, s := range f.size {
			f.encode = append(f.encode, 0, float64(s-1))
		}
	}
	f.decode = r.floatArray(dict.Get("Decode"))
	if len(f.decode) != len(base.rng) {
		f.decode = base.rng
	}
	return f
}

// eval reads the grid at the point the inputs name, mixing the samples on
// either side of it along every axis.
func (f *sampledFn) eval(in []float64) []float64 {
	x := f.clipDomain(in)
	// Where in the grid each input falls, and how far between two rows.
	idx := make([]int, len(x))
	frac := make([]float64, len(x))
	for i, v := range x {
		e := interpolate(v, f.domain[2*i], f.domain[2*i+1], f.encode[2*i], f.encode[2*i+1])
		e = clampTo(e, 0, float64(f.size[i]-1))
		idx[i] = int(math.Floor(e))
		if idx[i] > f.size[i]-2 {
			idx[i] = maxInt(f.size[i]-2, 0)
		}
		frac[i] = e - float64(idx[i])
	}
	out := make([]float64, f.n)
	maxValue := float64(uint64(1)<<uint(f.bps) - 1)
	// Every corner of the cell the point sits in, weighted by how near it is.
	corners := 1 << uint(len(x))
	for c := 0; c < corners; c++ {
		weight := 1.0
		at := make([]int, len(x))
		for i := range x {
			if c&(1<<uint(i)) != 0 {
				at[i] = minInt(idx[i]+1, f.size[i]-1)
				weight *= frac[i]
			} else {
				at[i] = idx[i]
				weight *= 1 - frac[i]
			}
		}
		if weight == 0 {
			continue
		}
		off := f.offsetOf(at)
		for j := 0; j < f.n; j++ {
			out[j] += weight * float64(f.sampleAt(off*f.n+j))
		}
	}
	for j := 0; j < f.n; j++ {
		out[j] = interpolate(out[j], 0, maxValue, f.decode[2*j], f.decode[2*j+1])
	}
	return f.clipRange(out)
}

// offsetOf is which sample of the grid a set of indices names, counting the
// first input fastest.
func (f *sampledFn) offsetOf(at []int) int {
	off, stride := 0, 1
	for i := range at {
		off += at[i] * stride
		stride *= f.size[i]
	}
	return off
}

// sampleAt reads one number out of the packed grid.
func (f *sampledFn) sampleAt(i int) uint64 {
	bit := i * f.bps
	var v uint64
	for k := 0; k < f.bps; k++ {
		b := bit + k
		j := b / 8
		if j >= len(f.samples) {
			return v << uint(f.bps-k)
		}
		v = v<<1 | uint64(f.samples[j]>>(7-b%8)&1)
	}
	return v
}

// An exponential function is a curve between two colours: at nothing it is the
// first, at one it is the second, and in between it is whatever the exponent
// says.
type exponential struct {
	functionBase
	c0, c1 []float64
	n      float64
}

func (f *exponential) outputs() int { return len(f.c0) }

// readExponential reads a function type 2.
func (r *renderer) readExponential(base functionBase, dict reader.Dict) function {
	f := &exponential{functionBase: base, n: 1}
	if v, ok := reader.ToFloat(resolve(r.doc, dict.Get("N"))); ok {
		f.n = v
	}
	f.c0 = r.floatArray(dict.Get("C0"))
	f.c1 = r.floatArray(dict.Get("C1"))
	if f.c0 == nil {
		f.c0 = []float64{0}
	}
	if f.c1 == nil {
		f.c1 = []float64{1}
	}
	if len(f.c0) != len(f.c1) || len(f.c0) == 0 {
		return nil
	}
	return f
}

func (f *exponential) eval(in []float64) []float64 {
	x := f.clipDomain(in)
	t := 0.0
	if len(x) > 0 {
		t = x[0]
	}
	// A negative base with a fractional exponent has no real value; the
	// domain of such a function starts at nothing, and clipping to zero is
	// what every reader does with one that does not.
	p := t
	if f.n != 1 {
		if t < 0 {
			t = 0
		}
		p = math.Pow(t, f.n)
	}
	out := make([]float64, len(f.c0))
	for i := range out {
		out[i] = f.c0[i] + p*(f.c1[i]-f.c0[i])
	}
	return f.clipRange(out)
}

// A stitching function is several functions laid end to end along the input,
// each taking over where the last left off.
type stitching struct {
	functionBase
	parts  []function
	bounds []float64
	encode []float64
}

func (f *stitching) outputs() int {
	if len(f.rng) >= 2 {
		return len(f.rng) / 2
	}
	return f.parts[0].outputs()
}

// readStitching reads a function type 3.
func (r *renderer) readStitching(base functionBase, dict reader.Dict, depth int) function {
	arr, ok := reader.ToArray(resolve(r.doc, dict.Get("Functions")))
	if !ok || len(arr) == 0 || len(base.domain) < 2 {
		return nil
	}
	f := &stitching{functionBase: base}
	for _, e := range arr {
		part := r.readOneFunction(e, depth+1)
		if part == nil {
			return nil
		}
		f.parts = append(f.parts, part)
	}
	f.bounds = r.floatArray(dict.Get("Bounds"))
	if len(f.bounds) != len(f.parts)-1 {
		return nil
	}
	f.encode = r.floatArray(dict.Get("Encode"))
	if len(f.encode) != 2*len(f.parts) {
		return nil
	}
	return f
}

func (f *stitching) eval(in []float64) []float64 {
	x := f.clipDomain(in)
	t := 0.0
	if len(x) > 0 {
		t = x[0]
	}
	// Which part covers this input, and what it spans.
	k := 0
	for k < len(f.bounds) && t >= f.bounds[k] {
		k++
	}
	lo, hi := f.domain[0], f.domain[1]
	if k > 0 {
		lo = f.bounds[k-1]
	}
	if k < len(f.bounds) {
		hi = f.bounds[k]
	}
	e := interpolate(t, lo, hi, f.encode[2*k], f.encode[2*k+1])
	return f.clipRange(f.parts[k].eval([]float64{e}))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
