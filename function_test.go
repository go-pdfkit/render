package render

import (
	"math"
	"testing"

	"github.com/go-pdfkit/reader"
)

// fnDoc builds a document holding one function so that it can be read the way
// a page would read it, and gives back the function.
func fnDoc(t *testing.T, build func(w *reader.Writer) reader.Object) function {
	t.Helper()
	r, obj := fnRenderer(t, build)
	return r.readFunction(obj, 0)
}

// fnRenderer builds the document and a renderer over it, so that a test can
// also read something that is not a function.
func fnRenderer(t *testing.T, build func(w *reader.Writer) reader.Object) (*renderer, reader.Object) {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	obj := build(w)
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)},
		"Held":     obj})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	return &renderer{doc: d, fonts: map[int]*pdfFont{}}, p.Get("Held")
}

// nums is an array of numbers, which is what most of a function dictionary is.
func nums(vs ...float64) reader.Array {
	out := make(reader.Array, 0, len(vs))
	for _, v := range vs {
		out = append(out, reader.Real(v))
	}
	return out
}

// close reports whether two lists of numbers agree to within a whisker.
func closeEnough(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-6 {
			return false
		}
	}
	return true
}

func TestAnExponentialFunction(t *testing.T) {
	// The second kind: a curve from one colour to another.
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(reader.Dict{
			"FunctionType": reader.Integer(2), "Domain": nums(0, 1),
			"C0": nums(1, 0, 0), "C1": nums(0, 0, 1), "N": reader.Integer(1),
		})
	})
	if f == nil {
		t.Fatal("an exponential function was not read")
	}
	if f.outputs() != 3 {
		t.Errorf("outputs() = %d", f.outputs())
	}
	for _, c := range []struct {
		in   float64
		want []float64
	}{
		{0, []float64{1, 0, 0}},
		{1, []float64{0, 0, 1}},
		{0.5, []float64{0.5, 0, 0.5}},
		{-3, []float64{1, 0, 0}}, // clipped to the domain
		{9, []float64{0, 0, 1}},
	} {
		if got := f.eval([]float64{c.in}); !closeEnough(got, c.want) {
			t.Errorf("at %v: %v, want %v", c.in, got, c.want)
		}
	}

	// An exponent other than one bends the curve, and a negative input to a
	// fractional power is taken as nothing rather than as no number at all.
	sq := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(reader.Dict{
			"FunctionType": reader.Integer(2), "Domain": nums(-1, 1),
			"N": reader.Real(0.5),
		})
	})
	if got := sq.eval([]float64{0.25}); !closeEnough(got, []float64{0.5}) {
		t.Errorf("the square root of a quarter came out as %v", got)
	}
	if got := sq.eval([]float64{-1}); !closeEnough(got, []float64{0}) {
		t.Errorf("a negative input gave %v", got)
	}

	// A function with no operands at all is evaluated at nothing.
	if got := sq.eval(nil); !closeEnough(got, []float64{0}) {
		t.Errorf("with no input it gave %v", got)
	}

	// The range clips the result.
	clipped := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(reader.Dict{
			"FunctionType": reader.Integer(2), "Domain": nums(0, 1),
			"Range": nums(0, 0.25), "C0": nums(0), "C1": nums(1),
		})
	})
	if got := clipped.eval([]float64{1}); !closeEnough(got, []float64{0.25}) {
		t.Errorf("the range did not clip: %v", got)
	}
}

func TestAStitchingFunction(t *testing.T) {
	// The third kind: several functions laid end to end.
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		first := w.Add(reader.Dict{"FunctionType": reader.Integer(2),
			"Domain": nums(0, 1), "C0": nums(0), "C1": nums(1)})
		second := w.Add(reader.Dict{"FunctionType": reader.Integer(2),
			"Domain": nums(0, 1), "C0": nums(1), "C1": nums(0)})
		return w.Add(reader.Dict{
			"FunctionType": reader.Integer(3), "Domain": nums(0, 1),
			"Functions": reader.Array{first, second},
			"Bounds":    nums(0.5), "Encode": nums(0, 1, 0, 1),
		})
	})
	if f == nil {
		t.Fatal("a stitching function was not read")
	}
	for _, c := range []struct{ in, want float64 }{
		{0, 0}, {0.25, 0.5}, {0.5, 1}, {0.75, 0.5}, {1, 0},
	} {
		if got := f.eval([]float64{c.in}); !closeEnough(got, []float64{c.want}) {
			t.Errorf("at %v: %v, want %v", c.in, got, c.want)
		}
	}
	if f.outputs() != 1 {
		t.Errorf("outputs() = %d", f.outputs())
	}
}

func TestASampledFunction(t *testing.T) {
	// The first kind: a grid of numbers read off with straight lines between.
	// Four samples of one byte each, from black to white and back.
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(&reader.Stream{
			Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Range": nums(0, 1), "Size": reader.Array{reader.Integer(3)},
				"BitsPerSample": reader.Integer(8),
			},
			Raw: []byte{0, 255, 0},
		})
	})
	if f == nil {
		t.Fatal("a sampled function was not read")
	}
	for _, c := range []struct{ in, want float64 }{
		{0, 0}, {0.5, 1}, {1, 0}, {0.25, 0.5}, {0.75, 0.5},
	} {
		if got := f.eval([]float64{c.in}); !closeEnough(got, []float64{c.want}) {
			t.Errorf("at %v: %v, want %v", c.in, got, c.want)
		}
	}

	// Two inputs read a grid rather than a row, and every sample width the
	// format allows is read the same way.
	for _, bps := range []int{1, 2, 4, 8, 16} {
		max := float64(uint64(1)<<uint(bps) - 1)
		grid := fnDoc(t, func(w *reader.Writer) reader.Object {
			var raw []byte
			switch bps {
			case 16:
				raw = []byte{0, 0, 0xff, 0xff}
			case 8:
				raw = []byte{0, 255}
			default:
				// Two samples packed into the top of one byte.
				raw = []byte{byte(uint64(max) << uint(8-bps))}
			}
			return w.Add(&reader.Stream{
				Dict: reader.Dict{
					"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
					"Range": nums(0, 1), "Size": reader.Array{reader.Integer(2)},
					"BitsPerSample": reader.Integer(int64(bps)),
				},
				Raw: raw,
			})
		})
		if grid == nil {
			t.Fatalf("%d bits a sample: not read", bps)
		}
		want := []float64{1}
		if bps < 8 {
			want = []float64{0} // the packed pair is high then low
		}
		got := grid.eval([]float64{1})
		if bps >= 8 && !closeEnough(got, want) {
			t.Errorf("%d bits a sample: at one it gave %v", bps, got)
		}
	}
}

func TestFunctionsThatAreNotOnes(t *testing.T) {
	cases := []struct {
		name  string
		build func(w *reader.Writer) reader.Object
	}{
		{"not a dictionary at all", func(w *reader.Writer) reader.Object {
			return reader.Integer(7)
		}},
		{"no function type", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Domain": nums(0, 1)})
		}},
		{"a type nobody has defined", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(9), "Domain": nums(0, 1)})
		}},
		{"no domain", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(2)})
		}},
		{"a domain that is not numbers", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(2),
				"Domain": reader.Array{reader.Name("x"), reader.Integer(1)}})
		}},
		{"an exponential whose two colours differ in length", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1),
				"C0": nums(0, 0), "C1": nums(1)})
		}},
		{"a sampled function that is not a stream", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Range": nums(0, 1), "Size": reader.Array{reader.Integer(2)},
				"BitsPerSample": reader.Integer(8)})
		}},
		{"a sampled function with no range", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Size": reader.Array{reader.Integer(2)}, "BitsPerSample": reader.Integer(8)},
				Raw: []byte{0, 255}})
		}},
		{"a sampled function with no size", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Range": nums(0, 1), "BitsPerSample": reader.Integer(8)},
				Raw: []byte{0, 255}})
		}},
		{"a sampled function with a size of nothing", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Range": nums(0, 1), "Size": reader.Array{reader.Integer(0)},
				"BitsPerSample": reader.Integer(8)}, Raw: []byte{0}})
		}},
		{"a sampled function bigger than any memory", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1, 0, 1, 0, 1),
				"Range":         nums(0, 1),
				"Size":          reader.Array{reader.Integer(1000), reader.Integer(1000), reader.Integer(1000)},
				"BitsPerSample": reader.Integer(8)}, Raw: []byte{0}})
		}},
		{"a sample width nothing uses", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Range": nums(0, 1), "Size": reader.Array{reader.Integer(2)},
				"BitsPerSample": reader.Integer(7)}, Raw: []byte{0, 255}})
		}},
		{"a sampled function with no sample width at all", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
				"Range": nums(0, 1), "Size": reader.Array{reader.Integer(2)}},
				Raw: []byte{0, 255}})
		}},
		{"a stitching function with no parts", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(3), "Domain": nums(0, 1),
				"Functions": reader.Array{}, "Bounds": nums(), "Encode": nums()})
		}},
		{"a stitching function whose parts are not functions", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(3), "Domain": nums(0, 1),
				"Functions": reader.Array{reader.Integer(3)}, "Bounds": nums(), "Encode": nums(0, 1)})
		}},
		{"a stitching function with the wrong number of bounds", func(w *reader.Writer) reader.Object {
			part := w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1)})
			return w.Add(reader.Dict{"FunctionType": reader.Integer(3), "Domain": nums(0, 1),
				"Functions": reader.Array{part}, "Bounds": nums(0.5), "Encode": nums(0, 1)})
		}},
		{"a stitching function with the wrong encoding", func(w *reader.Writer) reader.Object {
			part := w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1)})
			return w.Add(reader.Dict{"FunctionType": reader.Integer(3), "Domain": nums(0, 1),
				"Functions": reader.Array{part}, "Bounds": nums(), "Encode": nums(0)})
		}},
		{"a stitching function with no domain to stitch over", func(w *reader.Writer) reader.Object {
			part := w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1)})
			return w.Add(reader.Dict{"FunctionType": reader.Integer(3), "Domain": nums(0),
				"Functions": reader.Array{part}, "Bounds": nums(), "Encode": nums(0, 1)})
		}},
		{"a calculator that is not a stream", func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"FunctionType": reader.Integer(4), "Domain": nums(0, 1),
				"Range": nums(0, 1)})
		}},
		{"a calculator with no range", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{"FunctionType": reader.Integer(4),
				"Domain": nums(0, 1)}, Raw: []byte("{}")})
		}},
		{"a calculator that is not a program", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{"FunctionType": reader.Integer(4),
				"Domain": nums(0, 1), "Range": nums(0, 1)}, Raw: []byte("2 3 add")})
		}},
		{"a calculator whose braces do not match", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{"FunctionType": reader.Integer(4),
				"Domain": nums(0, 1), "Range": nums(0, 1)}, Raw: []byte("{ 2 3 add")})
		}},
		{"a calculator with something after its program", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{"FunctionType": reader.Integer(4),
				"Domain": nums(0, 1), "Range": nums(0, 1)}, Raw: []byte("{ 1 } 2")})
		}},
		{"a calculator with nothing in it at all", func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{"FunctionType": reader.Integer(4),
				"Domain": nums(0, 1), "Range": nums(0, 1)}, Raw: []byte("")})
		}},
		{"an array holding something that is not a function", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Integer(1)}
		}},
		{"an empty array", func(w *reader.Writer) reader.Object {
			return reader.Array{}
		}},
	}
	for _, c := range cases {
		if f := fnDoc(t, c.build); f != nil {
			t.Errorf("%s was read as a function", c.name)
		}
	}
}

func TestFunctionsSideBySide(t *testing.T) {
	// A shading may name one function per colour component rather than one
	// giving them all.
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		one := w.Add(reader.Dict{"FunctionType": reader.Integer(2),
			"Domain": nums(0, 1), "C0": nums(0), "C1": nums(1)})
		two := w.Add(reader.Dict{"FunctionType": reader.Integer(2),
			"Domain": nums(0, 1), "C0": nums(1), "C1": nums(0)})
		return reader.Array{one, two}
	})
	if f == nil {
		t.Fatal("an array of functions was not read")
	}
	if f.outputs() != 2 {
		t.Errorf("outputs() = %d", f.outputs())
	}
	if got := f.eval([]float64{0.25}); !closeEnough(got, []float64{0.25, 0.75}) {
		t.Errorf("at a quarter: %v", got)
	}
}

func TestAFunctionThatNamesItselfForEver(t *testing.T) {
	// A stitching function whose part is itself would go round for ever;
	// the depth it is read at is what stops it.
	r, _ := fnRenderer(t, func(w *reader.Writer) reader.Object { return reader.Null{} })
	if f := r.readOneFunction(reader.Null{}, maxFunctionDepth+1); f != nil {
		t.Error("a function nested past the limit was read")
	}
}

func TestFunctionsWhoseStreamCannotBeRead(t *testing.T) {
	// A function whose data is filtered as an image is not one, however well
	// formed the rest of its dictionary is.
	for _, kind := range []int64{0, 4} {
		f := fnDoc(t, func(w *reader.Writer) reader.Object {
			return w.Add(&reader.Stream{Dict: reader.Dict{
				"FunctionType":  reader.Integer(kind),
				"Domain":        nums(0, 1),
				"Range":         nums(0, 1),
				"Size":          reader.Array{reader.Integer(2)},
				"BitsPerSample": reader.Integer(8),
				"Filter":        reader.Name("DCTDecode"),
			}, Raw: []byte("not a jpeg")})
		})
		if f != nil {
			t.Errorf("a type %d function filtered as an image was read", kind)
		}
	}
}

func TestASampledFunctionShorterThanItSaysItIs(t *testing.T) {
	// A grid the stream does not hold all of: the samples that are there are
	// read and the rest come out as nothing, rather than reaching past the
	// end of the data.
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(&reader.Stream{Dict: reader.Dict{
			"FunctionType": reader.Integer(0), "Domain": nums(0, 1),
			"Range": nums(0, 1), "Size": reader.Array{reader.Integer(4)},
			"BitsPerSample": reader.Integer(12),
		}, Raw: []byte{0xff, 0xf0}})
	})
	if f == nil {
		t.Fatal("the function was not read")
	}
	if f.outputs() != 1 {
		t.Errorf("outputs() = %d", f.outputs())
	}
	if got := f.eval([]float64{1}); len(got) != 1 {
		t.Errorf("gave %v", got)
	}
}

func TestAStitchingFunctionThatSaysWhatItsRangeIs(t *testing.T) {
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		part := w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(0, 1),
			"C0": nums(0, 0), "C1": nums(1, 1)})
		return w.Add(reader.Dict{"FunctionType": reader.Integer(3), "Domain": nums(0, 1),
			"Range": nums(0, 1, 0, 1), "Functions": reader.Array{part},
			"Bounds": nums(), "Encode": nums(0, 1)})
	})
	if f == nil {
		t.Fatal("the function was not read")
	}
	if f.outputs() != 2 {
		t.Errorf("outputs() = %d, want 2", f.outputs())
	}
}

func TestADomainWrittenBackToFront(t *testing.T) {
	// A domain whose ends are the wrong way round still holds the input
	// between them, and one of no width at all gives the same answer
	// everywhere.
	f := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(reader.Dict{"FunctionType": reader.Integer(2), "Domain": nums(1, 0),
			"C0": nums(0), "C1": nums(1)})
	})
	if got := f.eval([]float64{9}); !closeEnough(got, []float64{1}) {
		t.Errorf("past the end of a backwards domain: %v", got)
	}
	flat := fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(&reader.Stream{Dict: reader.Dict{
			"FunctionType": reader.Integer(0), "Domain": nums(0.5, 0.5),
			"Range": nums(0, 1), "Size": reader.Array{reader.Integer(2)},
			"BitsPerSample": reader.Integer(8)}, Raw: []byte{0, 255}})
	})
	if flat == nil {
		t.Fatal("a function over a domain of no width was not read")
	}
	if got := flat.eval([]float64{0.5}); len(got) != 1 {
		t.Errorf("gave %v", got)
	}
}

func TestACalculatorWhoseBracesNestWrong(t *testing.T) {
	if f := calc(t, "{ {", 1); f != nil {
		t.Error("a program whose inner block never closes was read")
	}
	if f := calc(t, "{ pop dup }", 2); f != nil {
		// two outputs from one value is fine; this is only here to run the
		// dup with nothing under it, which is the next case.
		_ = f
	}
	if f := calc(t, "{ pop dup }", 1); f != nil {
		if got := f.eval([]float64{3}); len(got) != 1 {
			t.Errorf("gave %v", got)
		}
	}
}
