// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

// tripleCache remembers what a colour space made of a three-byte sample triple.
//
// A one-component picture gets a 256-entry memo, which is every answer there is.
// Three components have 16.7 million possible triples, which is not a memo -- but a
// PICTURE does not carry 16.7 million colours. Counted on the corpus's slowest
// remaining page, a 2313x2956 ICCBased RGB scan: 6 837 228 pixels and 208 801
// distinct triples, 3.05%. So 97 of every 100 conversions are a repeat.
//
// Two cheaper caches were measured first and both are too weak to write: the
// previous pixel's colour repeats on 6.8% of pixels and the last four on 14.5%. A
// photographic scan carries JPEG noise, so neighbours differ in the low bits.
//
// It is EXACT. What is stored is the answer the space gave for that exact triple,
// so the picture cannot change; only the number of times the space is asked does.
//
// OPEN ADDRESSING RATHER THAN A TWO-LEVEL TABLE, and that was measured too. A
// sparse table of 4 096 blocks of 4 096 entries gave the same 4x on that page and
// cost 36 MB of peak RSS, because 208 801 colours touch some 2 200 blocks and fill
// almost none of them -- an eleven-fold waste over the 3.3 MB the answers need.
// This holds one flat array of 2^19 slots at 8 bytes, 4 MB, and never grows: a
// triple that finds its slot taken after a few probes is simply converted again,
// which costs a conversion and not a correctness problem.
type tripleCache struct {
	// slot holds the key in the low 24 bits, the answer in the next 24, and a
	// used bit at the top, so one uint32 pair per entry is not needed and a zero
	// slot cannot be mistaken for a cached black.
	slot []uint64
}

// cacheSlots is how many entries the cache holds. 2^19 is four times the 131 072
// distinct colours a 300 dpi A4 scan of text carries and two and a half times the
// 208 801 of the heaviest page measured, which keeps the probe count near one.
const cacheSlots = 1 << 19

// maxProbes bounds the walk. A cache is allowed to miss: the answer is recomputed,
// and bounding the walk is what keeps a full table from turning a lookup into a
// scan of half a megabyte.
const maxProbes = 4

const cacheUsed = 1 << 48

// lookup returns the cached answer for one triple, and the slot to write it into
// when there is none. A slot of -1 means there was no room within maxProbes.
func (t *tripleCache) lookup(r, g, b uint8) (cr, cg, cb uint8, found bool, at int) {
	if t.slot == nil {
		t.slot = make([]uint64, cacheSlots)
	}
	key := tripleKey(r, g, b)
	h := slotFor(key)
	free := -1
	for p := 0; p < maxProbes; p++ {
		i := (h + p) & (cacheSlots - 1)
		s := t.slot[i]
		if s&cacheUsed == 0 {
			if free < 0 {
				free = i
			}
			break
		}
		if s&0xffffff == key {
			v := s >> 24
			return uint8(v >> 16), uint8(v >> 8), uint8(v), true, i
		}
	}
	return 0, 0, 0, false, free
}

// store writes one answer into the slot lookup named. A negative slot is a cache
// that had no room, and dropping the answer is the whole cost of that.
func (t *tripleCache) store(at int, r, g, b, cr, cg, cb uint8) {
	if at < 0 {
		return
	}
	key := uint64(r)<<16 | uint64(g)<<8 | uint64(b)
	val := uint64(cr)<<16 | uint64(cg)<<8 | uint64(cb)
	t.slot[at] = cacheUsed | val<<24 | key
}

// tripleKey packs one sample triple into the 24 bits the cache stores.
func tripleKey(r, g, b uint8) uint64 {
	return uint64(r)<<16 | uint64(g)<<8 | uint64(b)
}

// slotFor is where a key's probe run starts.
//
// Multiplicative hashing, and it is worth 26%: measured on the 6.8 megapixel scan
// this cache was written for, 205.5 ms against 259.4 ms for `key & (cacheSlots-1)`,
// same pixels. Nineteen low bits of a 24-bit triple throw away the top five bits of
// red, so a page whose red channel carries the variation piles thousands of colours
// into a few slots and misses almost every time.
//
// NO UNIT TEST PINS THIS, and pretending otherwise would be worse than saying so. A
// synthetic fill spread evenly enough to look like a picture does not collide under
// either hash; one tight enough to collide is an argument about the hash rather than
// about a picture. The justification is the corpus measurement above, which is
// reproducible against that page.
func slotFor(key uint64) int {
	return int((key * 0x9e3779b1) >> 13 & (cacheSlots - 1))
}
