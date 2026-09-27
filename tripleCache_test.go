// Copyright (c) 2026, the go-pdfkit/render authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package render

import "testing"

// TestACachedTripleComesBack.
func TestACachedTripleComesBack(t *testing.T) {
	var c tripleCache
	_, _, _, found, at := c.lookup(10, 20, 30)
	if found {
		t.Fatal("an empty cache answered")
	}
	c.store(at, 10, 20, 30, 1, 2, 3)
	r, g, b, found, _ := c.lookup(10, 20, 30)
	if !found {
		t.Fatal("what was stored did not come back")
	}
	if r != 1 || g != 2 || b != 3 {
		t.Errorf("got %d,%d,%d want 1,2,3", r, g, b)
	}
}

// TestACachedBlackIsNotAnEmptySlot. Zero is a legal answer AND a legal key, so a
// used bit is the only thing that tells them apart. Without it every black pixel
// would read as a miss, and an all-black picture would get no cache at all.
func TestACachedBlackIsNotAnEmptySlot(t *testing.T) {
	var c tripleCache
	_, _, _, _, at := c.lookup(0, 0, 0)
	c.store(at, 0, 0, 0, 0, 0, 0)
	if _, _, _, found, _ := c.lookup(0, 0, 0); !found {
		t.Error("black stored under the zero key read as an empty slot")
	}
}

// TestTwoTriplesThatCollideKeepTheirOwnAnswers. The defect this guards is the one
// that would be invisible in a render: a cache that answers for the wrong colour
// draws a picture that is wrong everywhere that colour appears.
func TestTwoTriplesThatCollideKeepTheirOwnAnswers(t *testing.T) {
	var c tripleCache
	// Find two triples that hash to the same slot rather than assuming any pair
	// does: a test built on a guessed collision proves nothing when the hash
	// changes. Asked of slotFor rather than of a cache, because probing through a
	// cache would allocate its table on every one of sixteen million tries.
	slotOf := func(r, g, b uint8) int { return slotFor(tripleKey(r, g, b)) }
	want := slotOf(1, 2, 3)
	var cr, cg, cb uint8
	found := false
	for v := 0; v < 1<<24 && !found; v++ {
		r, g, b := uint8(v>>16), uint8(v>>8), uint8(v)
		if r == 1 && g == 2 && b == 3 {
			continue
		}
		if slotOf(r, g, b) == want {
			cr, cg, cb, found = r, g, b, true
		}
	}
	if !found {
		t.Skip("no colliding triple found, which would mean a perfect hash")
	}
	_, _, _, _, at1 := c.lookup(1, 2, 3)
	c.store(at1, 1, 2, 3, 11, 22, 33)
	_, _, _, _, at2 := c.lookup(cr, cg, cb)
	c.store(at2, cr, cg, cb, 44, 55, 66)
	if at1 == at2 {
		t.Fatalf("the second triple took the first's slot %d", at1)
	}
	r, g, b, ok, _ := c.lookup(1, 2, 3)
	if !ok || r != 11 || g != 22 || b != 33 {
		t.Errorf("first triple came back %d,%d,%d (found %v), want 11,22,33", r, g, b, ok)
	}
	r, g, b, ok, _ = c.lookup(cr, cg, cb)
	if !ok || r != 44 || g != 55 || b != 66 {
		t.Errorf("colliding triple came back %d,%d,%d (found %v), want 44,55,66", r, g, b, ok)
	}
}

// TestACacheWithNoRoomRefusesRatherThanOverwrites. maxProbes bounds the walk, and
// what a bounded walk must NOT do is evict somebody else's answer: the caller
// recomputes, which costs a conversion, and a wrong colour would cost the picture.
func TestACacheWithNoRoomRefusesRatherThanOverwrites(t *testing.T) {
	var c tripleCache
	// Fill one run of maxProbes slots by hand.
	_, _, _, _, at := c.lookup(1, 2, 3)
	for i := 0; i < maxProbes; i++ {
		c.slot[(at+i)&(cacheSlots-1)] = cacheUsed | uint64(i+1)
	}
	_, _, _, found, where := c.lookup(1, 2, 3)
	if found {
		t.Fatal("a slot filled with other keys answered for this one")
	}
	if where >= 0 {
		t.Errorf("it offered slot %d in a full run, which would overwrite another answer", where)
	}
	// And storing into a negative slot must do nothing rather than panic.
	c.store(where, 1, 2, 3, 9, 9, 9)
	if _, _, _, found, _ := c.lookup(1, 2, 3); found {
		t.Error("a refused store was kept anyway")
	}
}

// TestTheCacheIsAllocatedOnFirstUseAndNotBefore, so a page with no such picture
// does not pay four megabytes for one it never reads.
func TestTheCacheIsAllocatedOnFirstUseAndNotBefore(t *testing.T) {
	var c tripleCache
	if c.slot != nil {
		t.Error("a fresh cache already holds its table")
	}
	c.lookup(0, 0, 0)
	if len(c.slot) != cacheSlots {
		t.Errorf("after one lookup the table is %d slots, want %d", len(c.slot), cacheSlots)
	}
}
