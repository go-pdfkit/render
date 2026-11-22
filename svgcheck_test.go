package render

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// The allowlist: every element the SVG device may write, and for each the
// attributes it may carry and the shape of every value. Anything else in the
// output — a script, a foreignObject, a style, an on* handler, an external
// reference, text — fails the gate, whatever the PDF said.
const (
	numRE   = `-?[0-9]+(\.[0-9]+)?(e[+-][0-9]+)?`
	idRE    = `[a-z][0-9]+`
	colorRE = `#[0-9a-f]{6}`
)

var svgAllowed = map[string]map[string]*regexp.Regexp{
	"svg": {
		"xmlns": re(`http://www\.w3\.org/2000/svg`), "width": re(numRE), "height": re(numRE),
		"viewBox": re(`0 0 [0-9]+ [0-9]+`),
	},
	"defs": {},
	"rect": {"width": re(`[0-9]+`), "height": re(`[0-9]+`), "fill": re(colorRE)},
	"g": {
		"clip-path": re(`url\(#` + idRE + `\)`), "opacity": re(numRE),
	},
	"clipPath": {"id": re(idRE), "clip-path": re(`url\(#` + idRE + `\)`)},
	"path": {
		"id": re(idRE), "d": re(`([MLQCZ ]|-?[0-9.]+)*`), "clip-rule": re(`evenodd`),
		"fill": re(colorRE + `|none`), "fill-rule": re(`evenodd`), "fill-opacity": re(numRE),
		"stroke": re(colorRE), "stroke-opacity": re(numRE), "stroke-width": re(numRE),
		"stroke-linecap": re(`round|square`), "stroke-linejoin": re(`round|bevel`),
		"stroke-miterlimit": re(numRE), "stroke-dasharray": re(`(` + numRE + `)( ` + numRE + `)*`),
		"stroke-dashoffset": re(numRE),
	},
	"use": {
		"href": re(`#` + idRE), "transform": re(`matrix\((` + numRE + `)( ` + numRE + `){5}\)`),
		"fill": re(colorRE), "fill-opacity": re(numRE), "fill-rule": re(`evenodd`), "opacity": re(numRE),
	},
	"image": {
		"id": re(idRE), "x": re(`[0-9]+`), "y": re(`[0-9]+`), "width": re(`[0-9]+`), "height": re(`[0-9]+`),
		"href": re(`data:image/(png|jpeg);base64,[A-Za-z0-9+/=]*`),
	},
}

func re(s string) *regexp.Regexp { return regexp.MustCompile(`^(` + s + `)$`) }

// checkSVGDocument parses an output with encoding/xml against the allowlist,
// and checks that every reference points at something the document defines.
func checkSVGDocument(t testing.TB, doc []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(doc))
	defined, used := map[string]bool{}, []string{}
	depth, root := 0, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the output is not well-formed XML: %v", err)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			attrs, ok := svgAllowed[tk.Name.Local]
			if !ok || (tk.Name.Space != "http://www.w3.org/2000/svg" && tk.Name.Space != "") {
				t.Fatalf("element %q is not on the allowlist", tk.Name.Local)
			}
			if depth == 0 {
				if tk.Name.Local != "svg" || root {
					t.Fatalf("the root is %q", tk.Name.Local)
				}
				root = true
			}
			depth++
			for _, a := range tk.Attr {
				pat, ok := attrs[a.Name.Local]
				if !ok || a.Name.Space != "" {
					t.Fatalf("attribute %q is not allowed on <%s>", a.Name.Local, tk.Name.Local)
				}
				if !pat.MatchString(a.Value) {
					v := a.Value
					if len(v) > 80 {
						v = v[:80]
					}
					t.Fatalf("<%s %s=%q> is not of the allowed shape", tk.Name.Local, a.Name.Local, v)
				}
				if a.Name.Local == "id" {
					defined[a.Value] = true
				}
				if a.Name.Local == "href" && strings.HasPrefix(a.Value, "#") {
					used = append(used, a.Value[1:])
				}
				if a.Name.Local == "clip-path" {
					used = append(used, a.Value[len("url(#"):len(a.Value)-1])
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if len(bytes.TrimSpace(tk)) != 0 {
				t.Fatalf("the output holds text: %q", tk)
			}
		default:
			t.Fatalf("the output holds a %T", tok)
		}
	}
	if !root || depth != 0 {
		t.Fatal("the output is not one element")
	}
	for _, u := range used {
		if !defined[u] {
			t.Fatalf("reference to #%s, which the document does not define", u)
		}
	}
	// Never NaN or an infinity, anywhere but inside base64 data.
	text := regexp.MustCompile(`base64,[A-Za-z0-9+/=]*`).ReplaceAllString(string(doc), "")
	if strings.Contains(text, "NaN") || strings.Contains(strings.ToLower(text), "inf") {
		t.Fatal("the output holds a NaN or an infinity")
	}
}

// svgOf writes a page as SVG and checks it.
func svgOf(t testing.TB, d *reader.Document, opt SVGOptions) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := PageSVG(d, 1, &buf, opt); err != nil {
		t.Fatal(err)
	}
	checkSVGDocument(t, buf.Bytes())
	return buf.Bytes()
}

// tapSVG writes a page the suite has drawn as SVG and checks it. A page the
// SVG writer refuses — it is drawn at a scale of its own, with a bound of its
// own — is not a failure of the document.
func tapSVG(t *testing.T, d *reader.Document) {
	t.Helper()
	var buf bytes.Buffer
	if err := PageSVG(d, 1, &buf, SVGOptions{}); err != nil {
		t.Logf("SVG refused: %v", err)
		return
	}
	checkSVGDocument(t, buf.Bytes())
}
