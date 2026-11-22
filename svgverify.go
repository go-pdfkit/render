package render

import (
	"bytes"
	"errors"
)

// errSVGEscaped says a document the device wrote is not one it is allowed to
// write. It cannot happen while the device is right; it is what stands between
// a defect in it and a page that carries something no PDF may put there.
var errSVGEscaped = errors.New("render: internal error: the SVG left its allowlist")

// svgElements is the allowlist: every element the SVG device may write, and for
// each the attributes it may carry with a check of the shape of the value.
// Anything else in a document — a script, a foreignObject, a style, an on*
// handler, an external reference, text — is refused, whatever the PDF said.
var svgElements = map[string]map[string]func([]byte) bool{
	"svg":      {"xmlns": exactly("http://www.w3.org/2000/svg"), "width": isNum, "height": isNum, "viewBox": isViewBox},
	"defs":     {},
	"rect":     {"width": isUint, "height": isUint, "fill": isColour},
	"g":        {"clip-path": isURLRef, "opacity": isNum},
	"clipPath": {"id": isID, "clip-path": isURLRef},
	"path": {
		"id": isID, "d": isPathData, "clip-rule": exactly("evenodd"),
		"fill": isColourOrNone, "fill-rule": exactly("evenodd"), "fill-opacity": isNum,
		"stroke": isColour, "stroke-opacity": isNum, "stroke-width": isNum,
		"stroke-linecap": oneOf("round", "square"), "stroke-linejoin": oneOf("round", "bevel"),
		"stroke-miterlimit": isNum, "stroke-dasharray": isNumList, "stroke-dashoffset": isNum,
	},
	"use": {
		"href": isHref, "transform": isMatrix, "fill": isColour, "fill-opacity": isNum,
		"fill-rule": exactly("evenodd"), "opacity": isNum,
	},
	"image": {"id": isID, "width": isUint, "height": isUint, "href": isDataURI},
}

// verifySVG reads a document the device wrote, the way an XML reader would, and
// refuses it unless every element and attribute is on the allowlist, every value
// has its shape, there is no text, and every reference names an id the document
// defines. It allocates nothing but the ids it records.
func verifySVG(doc []byte) error {
	type span struct{ from, to int }
	// The open elements are spans of the document, not copies of their names.
	var open []span
	defined := map[string]struct{}{}
	var used []span
	root := false
	i := 0
	for i < len(doc) {
		if doc[i] != '<' || i+1 >= len(doc) {
			return errSVGEscaped // text
		}
		i++
		if doc[i] == '/' {
			j := i + 1
			for j < len(doc) && doc[j] != '>' {
				j++
			}
			if j >= len(doc) || len(open) == 0 || !bytes.Equal(doc[i+1:j], doc[open[len(open)-1].from:open[len(open)-1].to]) {
				return errSVGEscaped
			}
			open = open[:len(open)-1]
			i = j + 1
			continue
		}
		j := i
		for j < len(doc) && doc[j] != ' ' && doc[j] != '/' && doc[j] != '>' {
			j++
		}
		name := doc[i:j]
		attrs, ok := svgElements[string(name)]
		// The root is the svg element, once, and nothing follows it.
		if !ok || (len(open) == 0 && (root || string(name) != "svg")) {
			return errSVGEscaped
		}
		root = true
		i = j
		for i < len(doc) && doc[i] == ' ' {
			i++
			k := i
			for k < len(doc) && doc[k] != '=' {
				k++
			}
			if k+1 >= len(doc) || doc[k+1] != '"' {
				return errSVGEscaped
			}
			end := k + 2
			for end < len(doc) && doc[end] != '"' {
				end++
			}
			if end >= len(doc) {
				return errSVGEscaped
			}
			attr, value := doc[i:k], doc[k+2:end]
			check, ok := attrs[string(attr)]
			if !ok || !check(value) {
				return errSVGEscaped
			}
			switch {
			case string(attr) == "id":
				if _, dup := defined[string(value)]; dup {
					return errSVGEscaped
				}
				defined[string(value)] = struct{}{}
			case string(attr) == "href" && string(name) == "use":
				used = append(used, span{k + 3, end})
			case string(attr) == "clip-path":
				used = append(used, span{k + 2 + len("url(#"), end - 1})
			}
			i = end + 1
		}
		switch {
		case i+1 < len(doc) && doc[i] == '/' && doc[i+1] == '>':
			i += 2
		case i < len(doc) && doc[i] == '>':
			open = append(open, span{j - len(name), j})
			i++
		default:
			return errSVGEscaped
		}
	}
	if !root || len(open) != 0 {
		return errSVGEscaped
	}
	for _, u := range used {
		if _, ok := defined[string(doc[u.from:u.to])]; !ok {
			return errSVGEscaped
		}
	}
	return nil
}

func exactly(s string) func([]byte) bool { return func(b []byte) bool { return string(b) == s } }

func oneOf(s ...string) func([]byte) bool {
	return func(b []byte) bool {
		for _, v := range s {
			if string(b) == v {
				return true
			}
		}
		return false
	}
}

func digits(b []byte) int {
	n := 0
	for n < len(b) && b[n] >= '0' && b[n] <= '9' {
		n++
	}
	return n
}

func isUint(b []byte) bool { return len(b) > 0 && digits(b) == len(b) }

// isNum is -?[0-9]+(\.[0-9]+)?(e[+-][0-9]+)?
func isNum(b []byte) bool {
	if len(b) > 0 && b[0] == '-' {
		b = b[1:]
	}
	n := digits(b)
	if n == 0 {
		return false
	}
	b = b[n:]
	if len(b) > 0 && b[0] == '.' {
		n = digits(b[1:])
		if n == 0 {
			return false
		}
		b = b[1+n:]
	}
	if len(b) > 0 && b[0] == 'e' {
		if len(b) < 2 || (b[1] != '+' && b[1] != '-') {
			return false
		}
		n = digits(b[2:])
		if n == 0 {
			return false
		}
		b = b[2+n:]
	}
	return len(b) == 0
}

// isNumList is numbers, one space between each.
func isNumList(b []byte) bool {
	for {
		k := 0
		for k < len(b) && b[k] != ' ' {
			k++
		}
		if !isNum(b[:k]) {
			return false
		}
		if k == len(b) {
			return true
		}
		b = b[k+1:]
	}
}

func isMatrix(b []byte) bool {
	const pre, post = "matrix(", ")"
	if len(b) < len(pre)+len(post) || string(b[:len(pre)]) != pre || string(b[len(b)-1:]) != post {
		return false
	}
	list := b[len(pre) : len(b)-1]
	spaces := 0
	for _, c := range list {
		if c == ' ' {
			spaces++
		}
	}
	return spaces == 5 && isNumList(list)
}

func isViewBox(b []byte) bool {
	if len(b) < 4 || string(b[:4]) != "0 0 " {
		return false
	}
	rest := b[4:]
	for k, c := range rest {
		if c == ' ' {
			return isUint(rest[:k]) && isUint(rest[k+1:])
		}
	}
	return false
}

// isID is [a-z][0-9]+
func isID(b []byte) bool { return len(b) > 1 && b[0] >= 'a' && b[0] <= 'z' && isUint(b[1:]) }

func isHref(b []byte) bool { return len(b) > 0 && b[0] == '#' && isID(b[1:]) }

func isURLRef(b []byte) bool {
	return len(b) > len("url(#)") && string(b[:5]) == "url(#" && b[len(b)-1] == ')' && isID(b[5:len(b)-1])
}

func isColour(b []byte) bool {
	if len(b) != 7 || b[0] != '#' {
		return false
	}
	for _, c := range b[1:] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func isColourOrNone(b []byte) bool { return string(b) == "none" || isColour(b) }

// isPathData is the letters of the four segments and a close, spaces, and the
// characters a number is written with: no letter that could spell NaN or an
// infinity, and no quote or bracket.
func isPathData(b []byte) bool {
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9', c == ' ', c == '-', c == '.', c == 'M', c == 'L', c == 'Q', c == 'C', c == 'Z':
		default:
			return false
		}
	}
	return true
}

// isDataURI is a PNG or a JPEG as base64, and nothing else.
func isDataURI(b []byte) bool {
	for _, pre := range []string{"data:image/png;base64,", "data:image/jpeg;base64,"} {
		if len(b) >= len(pre) && string(b[:len(pre)]) == pre {
			for _, c := range b[len(pre):] {
				switch {
				case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/', c == '=':
				default:
					return false
				}
			}
			return true
		}
	}
	return false
}
