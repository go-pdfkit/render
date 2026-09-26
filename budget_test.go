package render

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

// slowPage builds a page that asks for a great deal of drawing: many thousands
// of filled rectangles, which is what a plotting tool writes and what takes a
// renderer a long time.
func slowPage(t *testing.T, rects int) *reader.Document {
	t.Helper()
	var content strings.Builder
	for i := 0; i < rects; i++ {
		fmt.Fprintf(&content, "%d %d 40 40 re f\n", i%60, (i*7)%60)
	}
	return shadedPage(t, content.String(), func(w *reader.Writer) reader.Dict {
		return reader.Dict{}
	})
}

func TestAPageMayBeGivenOnlySoLong(t *testing.T) {
	// Some pages take a very long time, and a caller drawing somebody else's
	// file cannot afford to wait for the worst of them. What comes back is
	// how far it got, and an error saying so.
	const rects = 40000

	// The same page drawn with NO limit, as the control. The bound below is a
	// ratio against that and not a number of seconds, and comparing the page
	// against itself is what makes it machine-independent: everything fixed
	// about drawing this page -- reading the stream, allocating the raster --
	// is paid by both runs and cancels.
	//
	// A fixed number of seconds is that quantity measured at the speed of one
	// machine. This test used to allow five of them, and the riscv64, s390x and
	// 386 lanes failed on it -- on the bound, not on a defect: 5.4 s, 8.3 s and
	// 11.3 s of emulated time for the same correct code. Measured here, a 50 ms
	// budget over 40 000 rectangles comes back in a ninth of the whole draw.
	whole := time.Now()
	if _, err := Page(slowPage(t, rects), 1, Options{Scale: 1}); err != nil {
		t.Fatal(err)
	}
	unbounded := time.Since(whole)

	d := slowPage(t, rects)
	start := time.Now()
	img, err := Page(d, 1, Options{Scale: 1, MaxDuration: 50 * time.Millisecond})
	took := time.Since(start)
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("a page given fifty milliseconds came back with %v after %s", err, took)
	}
	if img == nil {
		t.Fatal("nothing came back at all; half a page is worth more than none")
	}
	if img.W == 0 || img.H == 0 {
		t.Fatalf("what came back is %dx%d", img.W, img.H)
	}
	// It has to stop near when it was told to, not merely eventually.
	if took > unbounded/3 {
		t.Errorf("it took %s to give up on fifty milliseconds, against %s to draw the same page whole",
			took, unbounded)
	}
}

func TestAPageGivenNoLimitIsDrawnWhole(t *testing.T) {
	// Zero means as long as it takes, which is what this did before there was
	// anywhere to say otherwise.
	d := slowPage(t, 200)
	img, err := Page(d, 1, Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if isWhite(img, 20, 30) {
		t.Error("the page came back blank")
	}
}

func TestAPageThatFinishesInTimeSaysNothingAboutIt(t *testing.T) {
	d := slowPage(t, 50)
	img, err := Page(d, 1, Options{Scale: 1, MaxDuration: time.Minute})
	if err != nil {
		t.Fatalf("a page with a minute to draw in came back with %v", err)
	}
	if isWhite(img, 20, 30) {
		t.Error("the page came back blank")
	}
}

func TestLookingAtTheClock(t *testing.T) {
	// The clock is looked at once every so many operations, because asking the
	// machine the time is dear beside drawing a line. Once the time has gone,
	// it is gone: the answer does not depend on being asked again.
	r := &renderer{}
	if r.overrun() {
		t.Error("a page with no deadline said it had run out of time")
	}
	r.deadline = time.Now().Add(-time.Second)
	for i := 0; i < timeCheckEvery-1; i++ {
		if r.overrun() {
			t.Fatalf("it looked at the clock after %d operations, not %d", i+1, timeCheckEvery)
		}
	}
	if !r.overrun() {
		t.Fatal("it never looked at the clock at all")
	}
	if !r.overrun() {
		t.Error("having run out of time, it changed its mind")
	}

	fresh := &renderer{deadline: time.Now().Add(time.Hour)}
	for i := 0; i < timeCheckEvery+2; i++ {
		if fresh.overrun() {
			t.Fatal("a page with an hour to draw in said its time had gone")
		}
	}
}
