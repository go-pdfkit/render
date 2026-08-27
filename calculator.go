package render

import (
	"math"
	"strconv"

	"github.com/go-pdfkit/reader"
)

// A calculator is a function written as a little PostScript program: the
// fourth kind, and the only one that computes rather than looks up. It is a
// deliberately small language — arithmetic, comparison, and a conditional —
// with no way to loop, so a program either finishes or was never valid.
type calculator struct {
	functionBase
	body []psOp
	n    int
}

func (f *calculator) outputs() int { return f.n }

// A psOp is one step of such a program: a number to push, an operator to run,
// or a block of steps to run conditionally.
type psOp struct {
	number float64
	name   string
	// yes and no are the blocks an if or an ifelse chooses between.
	yes, no []psOp
	isBlock bool
}

// maxCalculatorStack is what the specification allows a program to hold.
const maxCalculatorStack = 100

// readCalculator reads a function type 4 and compiles its program.
func (r *renderer) readCalculator(base functionBase, dict reader.Dict, stream *reader.Stream) function {
	if stream == nil || len(base.rng) < 2 {
		return nil
	}
	data, img, err := r.salvaged(stream)
	if err != nil || img != "" {
		return nil
	}
	toks := psTokens(data)
	// The whole program is wrapped in one pair of braces.
	if len(toks) < 2 || toks[0] != "{" {
		return nil
	}
	body, rest, ok := psParse(toks[1:])
	if !ok || len(rest) != 0 {
		return nil
	}
	return &calculator{functionBase: base, body: body, n: len(base.rng) / 2}
}

// psTokens cuts the program into braces, names and numbers, dropping the
// comments a program may carry.
func psTokens(b []byte) []string {
	var out []string
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c == '%':
			for i < len(b) && b[i] != '\n' && b[i] != '\r' {
				i++
			}
		case c == '{' || c == '}':
			out = append(out, string(c))
			i++
		case c <= ' ':
			i++
		default:
			start := i
			for i < len(b) && b[i] > ' ' && b[i] != '{' && b[i] != '}' && b[i] != '%' {
				i++
			}
			out = append(out, string(b[start:i]))
		}
	}
	return out
}

// psParse turns a run of tokens into steps, up to the brace that closes the
// block it is in.
func psParse(toks []string) (body []psOp, rest []string, ok bool) {
	for len(toks) > 0 {
		t := toks[0]
		toks = toks[1:]
		switch t {
		case "}":
			return body, toks, true
		case "{":
			inner, after, ok := psParse(toks)
			if !ok {
				return nil, nil, false
			}
			body = append(body, psOp{yes: inner, isBlock: true})
			toks = after
		default:
			if v, err := strconv.ParseFloat(t, 64); err == nil {
				body = append(body, psOp{number: v, name: "#"})
				continue
			}
			body = append(body, psOp{name: t})
		}
	}
	// A program that runs out of tokens before its closing brace is not one.
	return nil, nil, false
}

func (f *calculator) eval(in []float64) []float64 {
	st := append([]float64{}, f.clipDomain(in)...)
	st = psRun(f.body, st)
	// The outputs are the last of what is left on the stack, in order.
	out := make([]float64, f.n)
	for i := 0; i < f.n; i++ {
		j := len(st) - f.n + i
		if j >= 0 && j < len(st) {
			out[i] = st[j]
		}
	}
	return f.clipRange(out)
}

// psRun executes a block. A step that asks for more numbers than are there is
// passed over: the program was malformed, and what it has computed so far is
// worth more than nothing.
func psRun(body []psOp, st []float64) []float64 {
	// blocks holds the procedures pushed but not yet chosen between, since
	// an if or an ifelse names them by position rather than by value.
	var blocks [][]psOp
	for _, op := range body {
		if op.isBlock {
			blocks = append(blocks, op.yes)
			continue
		}
		if op.name == "#" {
			st = psPush(st, op.number)
			continue
		}
		switch op.name {
		case "if":
			if len(blocks) < 1 || len(st) < 1 {
				blocks = nil
				continue
			}
			cond := st[len(st)-1] != 0
			st = st[:len(st)-1]
			if cond {
				st = psRun(blocks[len(blocks)-1], st)
			}
			blocks = blocks[:len(blocks)-1]
		case "ifelse":
			if len(blocks) < 2 || len(st) < 1 {
				blocks = nil
				continue
			}
			cond := st[len(st)-1] != 0
			st = st[:len(st)-1]
			if cond {
				st = psRun(blocks[len(blocks)-2], st)
			} else {
				st = psRun(blocks[len(blocks)-1], st)
			}
			blocks = blocks[:len(blocks)-2]
		default:
			st = psApply(op.name, st)
		}
	}
	return st
}

// psPush puts a number on the stack, unless the program has already put more
// there than one is allowed to hold.
func psPush(st []float64, v float64) []float64 {
	if len(st) >= maxCalculatorStack {
		return st
	}
	return append(st, v)
}

// psApply runs one arithmetic, comparison or stack operator, giving back the
// stack as it now stands.
func psApply(name string, st []float64) []float64 {
	switch name {
	case "add", "sub", "mul", "div", "idiv", "mod", "atan", "exp",
		"eq", "ne", "gt", "ge", "lt", "le", "and", "or", "xor", "bitshift":
		if len(st) < 2 {
			return st
		}
		a, b := st[len(st)-2], st[len(st)-1]
		return psBinary(name, a, b, st[:len(st)-2])
	case "neg", "abs", "sqrt", "sin", "cos", "ln", "log", "cvi", "cvr",
		"floor", "ceiling", "round", "truncate", "not":
		if len(st) < 1 {
			return st
		}
		return psUnary(name, st[len(st)-1], st[:len(st)-1])
	case "dup":
		if len(st) < 1 {
			return st
		}
		return psPush(st, st[len(st)-1])
	case "pop":
		if len(st) < 1 {
			return st
		}
		return st[:len(st)-1]
	case "exch":
		if len(st) < 2 {
			return st
		}
		st[len(st)-2], st[len(st)-1] = st[len(st)-1], st[len(st)-2]
		return st
	case "copy":
		if len(st) < 1 {
			return st
		}
		k := int(st[len(st)-1])
		st = st[:len(st)-1]
		if k <= 0 || k > len(st) || len(st)+k > maxCalculatorStack {
			return st
		}
		return append(st, st[len(st)-k:]...)
	case "index":
		if len(st) < 1 {
			return st
		}
		k := int(st[len(st)-1])
		st = st[:len(st)-1]
		if k < 0 || k >= len(st) {
			return st
		}
		return psPush(st, st[len(st)-1-k])
	case "roll":
		return psRoll(st)
	case "true":
		return psPush(st, 1)
	case "false":
		return psPush(st, 0)
	}
	// An operator this language has not got: the program was not one.
	return st
}

// psBinary runs the operators that take two numbers, off a stack they have
// already been taken from.
func psBinary(name string, a, b float64, st []float64) []float64 {
	var v float64
	switch name {
	case "add":
		v = a + b
	case "sub":
		v = a - b
	case "mul":
		v = a * b
	case "div":
		// Dividing by nothing gives an infinity, which the function's range
		// then clips to whatever it says the largest output is. That is what
		// the other readers do, and it keeps the stack the depth the rest of
		// the program expects.
		v = a / b
	case "idiv":
		// Whole-number division by nothing has no answer at all, so it gives
		// nothing rather than an infinity.
		if int(b) != 0 {
			v = float64(int(a) / int(b))
		}
	case "mod":
		if int(b) != 0 {
			v = float64(int(a) % int(b))
		}
	case "atan":
		// The angle is in degrees, and always between nothing and a full turn.
		v = math.Atan2(a, b) * 180 / math.Pi
		if v < 0 {
			v += 360
		}
	case "exp":
		v = math.Pow(a, b)
	case "eq":
		v = psBool(a == b)
	case "ne":
		v = psBool(a != b)
	case "gt":
		v = psBool(a > b)
	case "ge":
		v = psBool(a >= b)
	case "lt":
		v = psBool(a < b)
	case "le":
		v = psBool(a <= b)
	case "and":
		v = float64(int(a) & int(b))
	case "or":
		v = float64(int(a) | int(b))
	case "xor":
		v = float64(int(a) ^ int(b))
	case "bitshift":
		k := int(b)
		if k >= 0 {
			v = float64(int(a) << uint(minInt(k, 63)))
		} else {
			v = float64(int(a) >> uint(minInt(-k, 63)))
		}
	}
	return psPush(st, v)
}

// psUnary runs the operators that take one number.
func psUnary(name string, x float64, st []float64) []float64 {
	var v float64
	switch name {
	case "neg":
		v = -x
	case "abs":
		v = math.Abs(x)
	case "sqrt":
		// A negative number has no square root here; the answer is nothing,
		// which keeps the stack the depth the rest of the program expects.
		if x >= 0 {
			v = math.Sqrt(x)
		}
	case "sin":
		v = math.Sin(x * math.Pi / 180)
	case "cos":
		v = math.Cos(x * math.Pi / 180)
	case "ln":
		if x > 0 {
			v = math.Log(x)
		}
	case "log":
		if x > 0 {
			v = math.Log10(x)
		}
	case "cvi", "truncate":
		v = math.Trunc(x)
	case "cvr":
		v = x
	case "floor":
		v = math.Floor(x)
	case "ceiling":
		v = math.Ceil(x)
	case "round":
		v = math.Round(x)
	case "not":
		// Applied to a truth value it negates it; applied to a number it
		// flips its bits, which is what PostScript does and what a function
		// using it as a mask expects.
		if x == 0 || x == 1 {
			v = psBool(x == 0)
		} else {
			v = float64(^int(x))
		}
	}
	return psPush(st, v)
}

// psRoll turns the top n numbers over by j places.
func psRoll(st []float64) []float64 {
	if len(st) < 2 {
		return st
	}
	j := int(st[len(st)-1])
	n := int(st[len(st)-2])
	st = st[:len(st)-2]
	if n <= 0 || n > len(st) {
		return st
	}
	part := st[len(st)-n:]
	j = ((j % n) + n) % n
	rolled := append(append([]float64{}, part[n-j:]...), part[:n-j]...)
	copy(part, rolled)
	return st
}

// psBool is how this language writes a truth value.
func psBool(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
