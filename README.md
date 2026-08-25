# render

[![CI](https://github.com/go-pdfkit/render/actions/workflows/ci.yml/badge.svg)](https://github.com/go-pdfkit/render/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-pdfkit/render.svg)](https://pkg.go.dev/github.com/go-pdfkit/render)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-pdfkit/render)](https://goreportcard.com/report/github.com/go-pdfkit/render)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing)

Turns a page of a PDF into pixels, in pure Go with no C anywhere.

It reads with [`reader`](https://github.com/go-pdfkit/reader), rasterises with
[`go-gfx/gfx`](https://github.com/go-gfx/gfx), and — where there is text —
shapes and outlines it with
[`go-opentype`](https://github.com/go-opentype/opentype). Nothing else: this
builds for `GOOS=js/wasm` and every architecture the fleet targets, which is
what lets a PDF be shown in a browser tab with nothing on the far end.

## What it draws

The graphics half of the format: the transform stack, path construction
and every one of the ten painting operators, filling under both winding
rules, stroking with the caps, joins, miter limit and dash pattern the
graphics state carries, clipping — including clips that intersect and are
let go again with `Q` — the device, calibrated, profile-based, indexed,
separation and pattern colour spaces, constant transparency, and form
XObjects with their own matrix, bounding box and resources.

Images too: XObjects and inline images, at every bit depth the format has,
in every colour space, with a `/Decode` array, as one-bit stencils painted
in the colour in force, and with either kind of transparency — a soft mask
of levels or a stencil of what to leave out. A JPEG is decoded by the
standard library; a format nothing here reads is left undrawn rather than
drawn wrong.

Text: the whole text state and every positioning and showing operator,
with glyphs taken from an embedded TrueType or OpenType font, from a
composite font addressed by character identifier, or — for a Type 3 font,
whose glyphs are little content streams — by running them. A font whose
program cannot be read still advances the pen by its stated widths, so
what follows the text stays where it belongs.

Type 1 font programs, the standard fourteen faces, shadings and tiling
patterns are the waves that follow. A

## How it is checked

By writing a PDF whose geometry is known and looking at the pixels that
come out: a rectangle from (10,10) to (40,40) on a hundred-point page has
to be ink at (25,75) in the image and paper at (25,55), which is what says
the page's coordinates — which count up from the bottom left — were mapped
the right way up. Rotation, a media box that does not start at the origin,
the crop box winning over the media box, every colour space, both winding
rules, clipping, transparency and forms are all checked the same way.

Then on the corpus, for robustness rather than correctness: **4 108 pages**
drawn from 3 999 real files with **no panics and no failures**. 26 of them
come out blank, which is the honest count of pages carrying nothing this
wave understands yet — it was 373 before images, and 31 before text.

## Testing

```sh
go test -covermode=set ./...
```

CI gates on **exact 100% statement coverage**, `go vet`, and a cross-compile
across `linux/{amd64,arm64,riscv64,loong64,ppc64le,s390x}`, `js/wasm`,
`darwin/arm64` and `windows/amd64`.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/render authors.
