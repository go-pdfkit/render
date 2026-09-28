// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

// tripleCache remembers what a colour conversion made of a sample tuple, keyed on
// the tuple's bytes packed into a uint32: three of them for an RGB triple, four for
// a CMYK quad.
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
//
// ONE TABLE PER PAGE, NOT PER PICTURE, and a regression is why. The first version
// declared it inside the loop that draws one image, so a page carrying many small
// ones allocated 4 MB for each. fr-cerfa's cerfa_12626.pdf carries 4 606 images on
// page 1, 2 112 of them three-component ICC JPEGs of EIGHT BY ONE pixels, and it
// went from 34.0 ms to 357.2 ms -- 10.5x slower, with the same pixels. The check
// added to `compare` that same day (-against with -confirm) reported it on its
// first real run, at 7.88x, already re-drawn three times and marked confirmed.
//
// Sharing one table across a page needs care, because the answer depends on the
// COLOUR SPACE and the /Decode array and not only on the triple: two images on one
// page may name different spaces. So each image takes a generation, and a slot
// written under an older one reads as empty. That way nothing is cleared and
// nothing is allocated twice -- clearing 4 MB costs what allocating it costs.
type tripleCache struct {
	// slot holds the key in the low 24 bits, the answer in the next 24, and the
	// generation above that, so a zero slot cannot be mistaken for a cached black
	// and a slot from the previous image cannot answer for this one.
	slot []uint64
	// gen is the current image's generation, never zero, so an untouched slot
	// (which is zero) belongs to no image.
	gen uint64
}

// nextImage makes every slot written so far read as empty, without touching them.
func (t *tripleCache) nextImage() {
	t.gen++
	// A generation that wrapped into the field below it would let an ancient slot
	// answer, so the table is CLEARED and counting starts again. Cleared rather
	// than dropped: cerfa_12626.pdf carries 4 606 images on one page, which wraps
	// an eight-bit generation eighteen times, and dropping the table would make
	// that eighteen allocations of four megabytes where a clear is a memset.
	if t.gen > maxGen {
		t.gen = 1
		clear(t.slot)
	}
}

// cacheSlots is how many entries the cache holds. 2^19 is four times the 131 072
// distinct colours a 300 dpi A4 scan of text carries and two and a half times the
// 208 801 of the heaviest page measured, which keeps the probe count near one. A
// DVLA form's CMYK scan has 48 370 of 8.7 million pixels, 0.56%, so it sits well
// inside the same table.
const cacheSlots = 1 << 19

// maxProbes bounds the walk. A cache is allowed to miss: the answer is recomputed,
// and bounding the walk is what keeps a full table from turning a lookup into a
// scan of half a megabyte.
const maxProbes = 4

// A slot is 33 bits of key, then 24 of answer, then 7 of generation.
//
// THIRTY-THREE, because the key carries WHICH KIND of tuple it is. A CMYK quad
// fills 32 bits, and a quad whose cyan is zero packs into exactly the bits an RGB
// triple uses: quadKey(0, A, B, C) and tripleKey(A, B, C) are the same number. One
// table serves both, so a page carrying a CMYK picture and a calibrated RGB one
// could read one's answers for the other -- rendering, in the wrong colours, only
// where the keys happen to meet.
//
// The generation would separate them, since each picture takes a new one. That is
// discipline: it holds only as long as every path remembers to ask for a generation,
// and a path that forgets produces a defect no test here could be made to show. A
// bit in the key makes the collision IMPOSSIBLE instead, and leaves the generation
// doing only what it was built for -- two pictures of the SAME kind whose answers
// differ because their colour space or /Decode array differs.
const (
	keyBits  = 33
	keyMask  = 1<<keyBits - 1
	genShift = keyBits + 24
	maxGen   = 1<<(64-genShift) - 1
	// kindQuad marks a CMYK key. A triple leaves it clear.
	kindQuad = 1 << 32
)

// lookup returns the cached answer for one triple, and the slot to write it into
// when there is none. A slot of -1 means there was no room within maxProbes.
func (t *tripleCache) lookup(key uint64) (cr, cg, cb uint8, found bool, at int) {
	if t.gen == 0 {
		t.gen = 1
	}
	if t.slot == nil {
		t.slot = make([]uint64, cacheSlots)
	}
	h := slotFor(key)
	free := -1
	for p := 0; p < maxProbes; p++ {
		i := (h + p) & (cacheSlots - 1)
		s := t.slot[i]
		if s>>genShift != t.gen {
			// Either never written or written for an earlier image: free either
			// way, and the run ends here because a key's own slot cannot lie
			// beyond a slot it could have taken.
			if free < 0 {
				free = i
			}
			break
		}
		if s&keyMask == key {
			v := s >> keyBits
			return uint8(v >> 16), uint8(v >> 8), uint8(v), true, i
		}
	}
	return 0, 0, 0, false, free
}

// store writes one answer into the slot lookup named. A negative slot is a cache
// that had no room, and dropping the answer is the whole cost of that.
func (t *tripleCache) store(at int, key uint64, cr, cg, cb uint8) {
	if at < 0 {
		return
	}
	val := uint64(cr)<<16 | uint64(cg)<<8 | uint64(cb)
	t.slot[at] = t.gen<<genShift | val<<keyBits | key
}

// tripleKey packs one RGB sample triple into a key, with the kind bit clear.
func tripleKey(r, g, b uint8) uint64 {
	return uint64(r)<<16 | uint64(g)<<8 | uint64(b)
}

// quadKey packs one CMYK sample quad into a key, with the kind bit set. Four bytes
// fill 32 bits, and the kind bit above them is what keeps quadKey(0, A, B, C) from
// being the same number as tripleKey(A, B, C).
func quadKey(c, m, y, k uint8) uint64 {
	return kindQuad | uint64(c)<<24 | uint64(m)<<16 | uint64(y)<<8 | uint64(k)
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
