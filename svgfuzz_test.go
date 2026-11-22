package render

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// FuzzPageSVG draws any content stream as SVG and holds what comes out to the
// allowlist: whatever the stream says, the document is well formed, holds
// only the elements and attributes the checker names, resolves every
// reference, and never prints NaN or an infinity. A page that runs out of
// time or room is a refusal, not a failure.
func FuzzPageSVG(f *testing.F) {
	for _, s := range []string{
		"1 0 0 rg 10 10 40 40 re f",
		"0 g 6 w [3 2] 0 d 10 20 m 90 20 l S",
		"q 10 10 50 50 re W n 0 0 1 rg 0 0 100 100 re f Q",
		"BT /F 24 Tf 5 30 Td (HAHA) Tj ET",
		"BT /F 12 Tf 1e30 0 0 1e30 0 0 Tm (A) Tj ET",
		"1e30 0 0 1e30 0 0 cm 0 0 1 1 re f",
		"nan inf 0 0 re f 0 0 m 1e400 1e400 l S",
		"10 10 m 20 20 30 30 40 40 c h W* n 0 0 100 100 re f*",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, content []byte) {
		if len(content) > 1<<12 {
			return
		}
		d := pageWithFont(t, string(content), nil)
		var out bytes.Buffer
		err := PageSVG(d, 1, &out, SVGOptions{MaxBytes: 1 << 20, RasterScale: 1, MaxDuration: 2 * time.Second})
		if errors.Is(err, ErrSVGTooLarge) {
			if out.Len() != 0 {
				t.Fatalf("a refused page wrote %d bytes", out.Len())
			}
			return
		}
		if err != nil && !errors.Is(err, ErrTimedOut) {
			t.Fatal(err)
		}
		checkSVGDocument(t, out.Bytes())
	})
}
