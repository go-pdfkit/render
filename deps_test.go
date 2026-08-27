package render

import (
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

// TestAFileWithAnAbsurdObjectNumberOpensAtOnce guards the dependency rather
// than this package's own code.
//
// reader v0.4.1 stopped answering "which objects call themselves a catalogue?"
// by counting from zero to the largest object number a file names. A file of
// 219 bytes with no trailer and no startxref can only be read by repairing it,
// and if the last object it declares is numbered 2 147 483 647 that is two
// thousand million map lookups for four objects: 21.2 seconds, and not one
// byte allocated, which is why no memory limit anywhere caught it.
//
// Requiring v0.4.0 here meant that a caller who asked only for this package
// got the defect: minimum version selection would give them the version this
// go.mod names. Nothing in the test suite noticed, because a package's own
// tests never see its callers' module graph.
//
// The file is built rather than committed, so this brings nobody else's PDF
// into the repository.
func TestAFileWithAnAbsurdObjectNumberOpensAtOnce(t *testing.T) {
	file := []byte("%PDF-1.4\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]>>endobj\n" +
		"2147483647 0 obj<</X 1>>endobj\n")
	done := make(chan error, 1)
	go func() {
		_, err := reader.Open(file)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the file did not open: %v", err)
		}
	case <-time.After(5 * time.Second):
		// Against reader v0.4.0 this takes twenty-one seconds. Five is far
		// more than the fixed version needs and far less than the defect.
		t.Fatal("219 bytes took more than five seconds to open: the reader " +
			"this module requires still walks to the largest object number")
	}
}
