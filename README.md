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

## Testing

```sh
go test -covermode=set ./...
```

CI gates on **exact 100% statement coverage**, `go vet`, and a cross-compile
across `linux/{amd64,arm64,riscv64,loong64,ppc64le,s390x}`, `js/wasm`,
`darwin/arm64` and `windows/amd64`.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/render authors.
