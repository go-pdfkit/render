package render

import (
	"github.com/go-pdfkit/reader"
)

// A function is what a PDF calls a function: a rule that turns some numbers
// into some others. Four kinds exist, and every one of them is used for
// something a page can show — the ramp of a gradient, the ink a spot colour
// stands for, the levels of a soft mask.
type function interface {
	// eval maps the inputs to the outputs, both already clipped to whatever
	// the function said its domain and range were.
	eval(in []float64) []float64
	// outputs is how many numbers come back, so a caller can size its buffer
	// before asking.
	outputs() int
}

// maxFunctionDepth bounds how deeply one function may name another, since a
// stitching function's parts are functions in their own right.
const maxFunctionDepth = 8

// readFunction reads whatever a function entry holds: one function, or an
// array of them standing side by side, each giving one output.
func (r *renderer) readFunction(o reader.Object, depth int) function {
	resolved := resolve(r.doc, o)
	if arr, ok := reader.ToArray(resolved); ok {
		parts := make([]function, 0, len(arr))
		for _, e := range arr {
			f := r.readOneFunction(e, depth)
			if f == nil {
				return nil
			}
			parts = append(parts, f)
		}
		if len(parts) == 0 {
			return nil
		}
		return &sideBySide{parts: parts}
	}
	return r.readOneFunction(resolved, depth)
}

// sideBySide is an array of functions, each contributing its outputs in turn.
// A shading names one this way when each colour component has a ramp of its
// own.
type sideBySide struct {
	parts []function
	n     int
}

func (s *sideBySide) outputs() int {
	if s.n == 0 {
		for _, p := range s.parts {
			s.n += p.outputs()
		}
	}
	return s.n
}

func (s *sideBySide) eval(in []float64) []float64 {
	out := make([]float64, 0, s.outputs())
	for _, p := range s.parts {
		out = append(out, p.eval(in)...)
	}
	return out
}

// readOneFunction reads a single function of whichever of the four kinds it
// declares itself to be.
func (r *renderer) readOneFunction(o reader.Object, depth int) function {
	if depth > maxFunctionDepth {
		return nil
	}
	resolved := resolve(r.doc, o)
	dict, ok := reader.ToDict(resolved)
	var stream *reader.Stream
	if s, isStream := reader.ToStream(resolved); isStream {
		dict, ok, stream = s.Dict, true, s
	}
	if !ok {
		return nil
	}
	kind, ok := reader.ToInt(resolve(r.doc, dict.Get("FunctionType")))
	if !ok {
		return nil
	}
	base := r.readFunctionBase(dict)
	if len(base.domain) == 0 {
		return nil
	}
	switch kind {
	case 0:
		return r.readSampled(base, dict, stream)
	case 2:
		return r.readExponential(base, dict)
	case 3:
		return r.readStitching(base, dict, depth)
	case 4:
		return r.readCalculator(base, dict, stream)
	}
	return nil
}

// A functionBase is what every function has: the range of inputs it is defined
// over, and — for all but the exponential kind — the range its outputs are
// clipped to.
type functionBase struct {
	domain []float64
	rng    []float64
}

// readFunctionBase reads /Domain and /Range.
func (r *renderer) readFunctionBase(dict reader.Dict) functionBase {
	return functionBase{
		domain: r.floatArray(dict.Get("Domain")),
		rng:    r.floatArray(dict.Get("Range")),
	}
}

// floatArray reads an array of numbers, or nothing.
func (r *renderer) floatArray(o reader.Object) []float64 {
	arr, ok := reader.ToArray(resolve(r.doc, o))
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, e := range arr {
		v, ok := reader.ToFloat(resolve(r.doc, e))
		if !ok {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// clipDomain holds the inputs to what the function says it is defined over.
func (b functionBase) clipDomain(in []float64) []float64 {
	out := make([]float64, len(b.domain)/2)
	for i := range out {
		v := 0.0
		if i < len(in) {
			v = in[i]
		}
		out[i] = clampTo(v, b.domain[2*i], b.domain[2*i+1])
	}
	return out
}

// clipRange holds the outputs to what the function says they can be. A
// function with no range stated leaves them alone.
func (b functionBase) clipRange(out []float64) []float64 {
	if len(b.rng) < 2*len(out) {
		return out
	}
	for i := range out {
		out[i] = clampTo(out[i], b.rng[2*i], b.rng[2*i+1])
	}
	return out
}

// clampTo holds a number between two others, whichever way round they were
// written.
func clampTo(v, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// interpolate maps a number from one interval onto another.
func interpolate(x, xmin, xmax, ymin, ymax float64) float64 {
	if xmax == xmin {
		return ymin
	}
	return ymin + (x-xmin)*(ymax-ymin)/(xmax-xmin)
}
