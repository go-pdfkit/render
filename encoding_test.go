package render

import "testing"

func TestRuneOfGlyphName(t *testing.T) {
	cases := []struct {
		name string
		want rune
		ok   bool
	}{
		{"A", 'A', true},
		{"space", ' ', true},
		{"eacute", 'é', true},
		{"uni0041", 'A', true},
		{"uni20AC", '€', true},
		{"u0041", 'A', true},
		{"u1F600", 0x1F600, true},
		{"uni00", 0, false},       // too few digits
		{"uni00000041", 0, false}, // too many
		{"uniZZZZ", 0, false},     // not digits
		{"nonesuch", 0, false},
		{"uni", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := runeOfGlyphName(c.name)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q = %q, %v — want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestHexDigit(t *testing.T) {
	for c, want := range map[byte]int{
		'0': 0, '9': 9, 'a': 10, 'f': 15, 'A': 10, 'F': 15, 'g': -1, ' ': -1,
	} {
		if got := hexDigit(c); got != want {
			t.Errorf("hexDigit(%q) = %d, want %d", c, got, want)
		}
	}
}

func TestTheEncodingTablesAgreeWithThemselves(t *testing.T) {
	// Every name either stands for a character or is written in one of the two
	// conventions; a name in neither could never be looked up.
	for _, table := range [][256]string{standardEncoding, winAnsiEncoding} {
		for code, name := range table {
			if name == "" {
				continue
			}
			if _, ok := runeOfGlyphName(name); !ok {
				t.Errorf("code %d is called %q, which stands for nothing", code, name)
			}
		}
	}
	// And the printable range of both tables agrees with ASCII.
	for code := 33; code < 127; code++ {
		if code == 39 || code == 96 {
			continue // the two quotes differ between the tables, by design
		}
		r, ok := runeOfGlyphName(winAnsiEncoding[code])
		if !ok || r != rune(code) {
			t.Errorf("WinAnsi %d is %q, which is %q", code, winAnsiEncoding[code], r)
		}
	}
}
