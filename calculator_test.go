package render

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// calc builds a calculator function from its program, with as many outputs as
// the range says.
func calc(t *testing.T, program string, outputs int) function {
	t.Helper()
	rng := make([]float64, 0, 2*outputs)
	for i := 0; i < outputs; i++ {
		rng = append(rng, -1e6, 1e6)
	}
	return fnDoc(t, func(w *reader.Writer) reader.Object {
		return w.Add(&reader.Stream{Dict: reader.Dict{
			"FunctionType": reader.Integer(4),
			"Domain":       nums(-1e6, 1e6),
			"Range":        nums(rng...),
		}, Raw: []byte(program)})
	})
}

func TestTheCalculatorArithmetic(t *testing.T) {
	// Every operator the little language has, checked one at a time. The
	// stack these leave behind is what the function gives back, so a
	// mistake in how it is kept shows up here rather than as a wrong colour
	// on a page.
	cases := []struct {
		program string
		in      float64
		want    []float64
	}{
		{"{ 2 add }", 3, []float64{5}},
		{"{ 2 sub }", 3, []float64{1}},
		{"{ 2 mul }", 3, []float64{6}},
		{"{ 2 div }", 3, []float64{1.5}},
		{"{ 0 div }", 3, []float64{1e6}}, // an infinity, clipped by the range
		{"{ 2 idiv }", 7, []float64{3}},
		{"{ 0 idiv }", 7, []float64{0}},
		{"{ 3 mod }", 7, []float64{1}},
		{"{ 0 mod }", 7, []float64{0}},
		{"{ 2 exp }", 3, []float64{9}},
		{"{ neg }", 3, []float64{-3}},
		{"{ abs }", -3, []float64{3}},
		{"{ sqrt }", 9, []float64{3}},
		{"{ sqrt }", -9, []float64{0}}, // no real root
		{"{ pop 90 sin }", 0, []float64{1}},
		{"{ pop 0 cos }", 0, []float64{1}},
		{"{ pop 1 ln }", 0, []float64{0}},
		{"{ ln }", -1, []float64{0}},
		{"{ pop 100 log }", 0, []float64{2}},
		{"{ log }", 0, []float64{0}},
		{"{ cvi }", 3.7, []float64{3}},
		{"{ cvr }", 3.5, []float64{3.5}},
		{"{ truncate }", -3.7, []float64{-3}},
		{"{ floor }", 3.7, []float64{3}},
		{"{ ceiling }", 3.2, []float64{4}},
		{"{ round }", 3.5, []float64{4}},
		{"{ pop 1 0 atan }", 0, []float64{90}},
		{"{ pop -1 0 atan }", 0, []float64{270}},
		{"{ 3 eq }", 3, []float64{1}},
		{"{ 3 ne }", 3, []float64{0}},
		{"{ 2 gt }", 3, []float64{1}},
		{"{ 3 ge }", 3, []float64{1}},
		{"{ 4 lt }", 3, []float64{1}},
		{"{ 3 le }", 3, []float64{1}},
		{"{ 6 and }", 3, []float64{2}},
		{"{ 6 or }", 3, []float64{7}},
		{"{ 6 xor }", 3, []float64{5}},
		{"{ 2 bitshift }", 3, []float64{12}},
		{"{ -1 bitshift }", 12, []float64{6}},
		{"{ 0 eq not }", 1, []float64{1}},
		{"{ not }", 6, []float64{-7}},
		{"{ pop true }", 0, []float64{1}},
		{"{ pop false }", 0, []float64{0}},
		{"{ dup add }", 3, []float64{6}},
		{"{ 7 exch sub }", 3, []float64{4}},
		{"{ 9 pop }", 3, []float64{3}},
		{"{ pop 1 2 3 3 1 roll }", 0, []float64{2}},
		{"{ 5 1 index add }", 0, []float64{5}},
		{"{ pop 1 2 2 copy add add add }", 0, []float64{6}},
	}
	for _, c := range cases {
		f := calc(t, c.program, len(c.want))
		if f == nil {
			t.Errorf("%s was not read", c.program)
			continue
		}
		if got := f.eval([]float64{c.in}); !closeEnough(got, c.want) {
			t.Errorf("%s at %v: %v, want %v", c.program, c.in, got, c.want)
		}
	}
}

func TestTheCalculatorChoosingBetweenTwoWays(t *testing.T) {
	// if and ifelse are the only branching the language has.
	f := calc(t, "{ 0.5 gt { 1 } { 0 } ifelse }", 1)
	if got := f.eval([]float64{0.9}); !closeEnough(got, []float64{1}) {
		t.Errorf("above a half: %v", got)
	}
	if got := f.eval([]float64{0.1}); !closeEnough(got, []float64{0}) {
		t.Errorf("below a half: %v", got)
	}
	g := calc(t, "{ dup 0.5 gt { pop 1 } if }", 1)
	if got := g.eval([]float64{0.9}); !closeEnough(got, []float64{1}) {
		t.Errorf("if above a half: %v", got)
	}
	if got := g.eval([]float64{0.25}); !closeEnough(got, []float64{0.25}) {
		t.Errorf("if below a half: %v", got)
	}
}

func TestACalculatorProgramThatIsMalformed(t *testing.T) {
	// A program that asks for more than is there is passed over rather than
	// refused: what it has computed so far is worth more than nothing.
	cases := []struct {
		program string
		want    []float64
	}{
		{"{ add }", []float64{0}},                // nothing to add
		{"{ pop pop }", []float64{0}},            // nothing to pop
		{"{ exch }", []float64{0}},               // nothing to swap
		{"{ dup }", []float64{0, 0}},             // one input duplicated
		{"{ pop copy }", []float64{0}},           // nothing to copy
		{"{ pop 0 copy }", []float64{0}},         // a copy of nothing
		{"{ pop 9 copy }", []float64{0}},         // more than is there
		{"{ pop index }", []float64{0}},          // nothing to index
		{"{ pop 9 index }", []float64{0}},        // past the end
		{"{ pop roll }", []float64{0}},           // nothing to roll
		{"{ pop 1 2 0 0 roll }", []float64{2}},   // rolling nothing
		{"{ pop 1 2 9 1 roll }", []float64{2}},   // more than is there
		{"{ if }", []float64{0}},                 // no block to run
		{"{ ifelse }", []float64{0}},             // nor two
		{"{ {1} if }", []float64{0}},             // no condition
		{"{ pop {1} {2} ifelse }", []float64{0}}, // nor for ifelse
		{"{ {1} ifelse }", []float64{0}},         // only one block
		{"{ frobnicate }", []float64{0}},         // an operator it has not got
		{"{ pop neg }", []float64{0}},            // nothing to negate
	}
	for _, c := range cases {
		f := calc(t, c.program, len(c.want))
		if f == nil {
			t.Errorf("%s was not read", c.program)
			continue
		}
		if got := f.eval([]float64{0}); !closeEnough(got, c.want) {
			t.Errorf("%s gave %v, want %v", c.program, got, c.want)
		}
	}
}

func TestACalculatorProgramThatPushesForEver(t *testing.T) {
	// The stack has a limit, and a program that goes past it is stopped
	// there rather than allowed to fill memory.
	program := "{ "
	for i := 0; i < maxCalculatorStack+50; i++ {
		program += "1 "
	}
	program += "}"
	f := calc(t, program, 1)
	if f == nil {
		t.Fatal("the program was not read")
	}
	if got := f.eval([]float64{0}); len(got) != 1 {
		t.Errorf("gave %d outputs", len(got))
	}
	// So is one that copies its way past it.
	g := calc(t, "{ pop 1 1 1 1 1 1 1 1 1 1 99 copy }", 1)
	if got := g.eval([]float64{0}); len(got) != 1 {
		t.Errorf("copying past the limit gave %d outputs", len(got))
	}
}

func TestACalculatorProgramWithCommentsInIt(t *testing.T) {
	f := calc(t, "{ % this is the ramp\n 2 mul % twice\n }", 1)
	if f == nil {
		t.Fatal("a program with comments was not read")
	}
	if got := f.eval([]float64{3}); !closeEnough(got, []float64{6}) {
		t.Errorf("gave %v", got)
	}
}

func TestTheTintTransformOfASeparationSpace(t *testing.T) {
	// The one that was found painting yellow: a Separation whose transform
	// is a little program mapping the tint onto the black plate. Reading it
	// wrong turns a white page yellow, which is what happened.
	r, obj := fnRenderer(t, func(w *reader.Writer) reader.Object {
		fn := w.Add(&reader.Stream{Dict: reader.Dict{
			"FunctionType": reader.Integer(4), "Domain": nums(0, 1),
			"Range": nums(0, 1, 0, 1, 0, 1, 0, 1),
		}, Raw: []byte("{dup 0 mul exch dup 0 mul exch dup 0 mul exch 1 mul }")})
		return reader.Array{reader.Name("Separation"), reader.Name("Black"),
			reader.Name("DeviceCMYK"), fn}
	})
	s := r.colourSpace(obj, nil, 0)
	if s == nil || s.components != 1 {
		t.Fatalf("the separation space was read as %v", s)
	}
	white := s.convert([]float64{0})
	if white.R != 255 || white.G != 255 || white.B != 255 {
		t.Errorf("a tint of nothing came out as %v, want white", white)
	}
	black := s.convert([]float64{1})
	if black.R != 0 || black.G != 0 || black.B != 0 {
		t.Errorf("a tint of one came out as %v, want black", black)
	}
}

func TestASeparationWhoseTransformCannotBeRead(t *testing.T) {
	// Such a space falls back on taking the tint for ink, which is right for
	// a single spot colour standing in for black.
	r, obj := fnRenderer(t, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("Separation"), reader.Name("Spot"),
			reader.Name("DeviceCMYK"), reader.Integer(7)}
	})
	s := r.colourSpace(obj, nil, 0)
	if s == nil {
		t.Fatal("the space was not read at all")
	}
	if got := s.convert([]float64{1}); got.R != 0 {
		t.Errorf("a full tint came out as %v", got)
	}
	if got := s.convert([]float64{0}); got.R != 255 {
		t.Errorf("no tint came out as %v", got)
	}
	// A DeviceN of several tints, likewise.
	r, obj = fnRenderer(t, func(w *reader.Writer) reader.Object {
		return reader.Array{reader.Name("DeviceN"),
			reader.Array{reader.Name("A"), reader.Name("B")},
			reader.Name("DeviceCMYK"), reader.Integer(7)}
	})
	s = r.colourSpace(obj, nil, 0)
	if s == nil || s.components != 2 {
		t.Fatalf("the DeviceN space was read as %v", s)
	}
}
