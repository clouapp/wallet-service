package deposit

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/app/services/chain"
)

func anchorTestSignature(name string) chain.AccountSignature {
	return chain.AccountSignature{Signature: name, Slot: 1}
}

// Each stretch end keeps the signature recorded right above it as the stretches move:
// what is recorded below an end is right below it (listings are contiguous runs), and
// the first signature recorded above a stretch of one is right above it.
func TestAccountState_KeepsTheAnchorOfEachStretchEnd(t *testing.T) {
	st := &accountState{account: "acc"}
	st.advance(anchorTestSignature("s9"), moveDown) // a first read, from the newest
	assert.Equal(t, "s9", st.bottom)
	assert.Empty(t, st.bottomAnchor, "the only signature of its stretch has nothing recorded above it")
	st.advance(anchorTestSignature("s8"), moveDown)
	st.advance(anchorTestSignature("s7"), moveDown)
	assert.Equal(t, "s7", st.bottom)
	assert.Equal(t, "s8", st.bottomAnchor)
	st.advance(anchorTestSignature("s10"), moveUp)
	assert.Equal(t, "s8", st.bottomAnchor, "a new signature above the stretch leaves its bottom's anchor")

	one := &accountState{account: "acc", top: "s1", bottom: "s1"}
	one.advance(anchorTestSignature("s2"), moveUp)
	one.advance(anchorTestSignature("s3"), moveUp)
	assert.Equal(t, "s1", one.bottom)
	assert.Equal(t, "s2", one.bottomAnchor, "the first signature recorded above a stretch of one, oldest first")

	empty := &accountState{account: "acc"}
	empty.advance(anchorTestSignature("e1"), moveUp) // an account known empty receives its first ones
	empty.advance(anchorTestSignature("e2"), moveUp)
	assert.Equal(t, "e1", empty.bottom)
	assert.Equal(t, "e2", empty.bottomAnchor)

	gap := &accountState{account: "acc", top: "s1", bottom: "s1"}
	gap.advance(anchorTestSignature("b9"), moveGapDown) // a burst, newest first
	assert.Empty(t, gap.aheadAnchor)
	gap.advance(anchorTestSignature("b8"), moveGapDown)
	gap.advance(anchorTestSignature("b7"), moveGapDown)
	assert.Equal(t, "b7", gap.aheadBottom)
	assert.Equal(t, "b8", gap.aheadAnchor)
	gap.closeGap() // the gap read ended right above s1
	assert.Equal(t, "b9", gap.top)
	assert.Equal(t, "s1", gap.bottom)
	assert.Equal(t, "b7", gap.bottomAnchor, "the stretch's oldest is right above the cursor's only signature")
	assert.Empty(t, gap.aheadAnchor)

	knownEmpty := &accountState{account: "acc"}
	knownEmpty.advance(anchorTestSignature("k5"), moveGapDown)
	knownEmpty.advance(anchorTestSignature("k4"), moveGapDown)
	knownEmpty.closeGap()
	assert.Equal(t, "k4", knownEmpty.bottom)
	assert.Equal(t, "k5", knownEmpty.bottomAnchor, "a cursor with nothing recorded takes the stretch and its anchor")

	single := &accountState{account: "acc", top: "s1", bottom: "s1", ahead: "a1", aheadBottom: "a1"}
	single.advance(anchorTestSignature("a2"), moveUp)
	assert.Equal(t, "a2", single.ahead)
	assert.Equal(t, "a2", single.aheadAnchor, "the first signature above a stretch ahead of one")
}

// A stretch of several signatures whose end has no anchor (a cursor saved before
// anchors were recorded) is shrunk to its newest signature before a listing below it:
// the cursor claims less, never more. A stretch of one keeps its end, read from the
// newest signature.
func TestSignatureCycle_ShrinksAStretchWithoutAnAnchorToItsNewestSignature(t *testing.T) {
	cycle := &signatureCycle{chainID: "tsol"}
	legacy := &accountState{account: "acc", top: "s9", bottom: "s5", endSeen: true, ahead: "a9", aheadBottom: "a5"}

	before, anchor := cycle.placeBottom(legacy)
	assert.Equal(t, "s9", before)
	assert.Empty(t, anchor)
	assert.False(t, legacy.endSeen, "the end seen below the old bottom says nothing below the new one")
	aheadBefore, aheadAnchor := cycle.placeAheadBottom(legacy)
	assert.Equal(t, "a9", aheadBefore)
	assert.Empty(t, aheadAnchor)
	assert.Equal(t, 2, legacy.unsaved)

	anchored := &accountState{account: "acc", top: "s9", bottom: "s5", bottomAnchor: "s6", ahead: "a9", aheadBottom: "a5", aheadAnchor: "a6"}
	before, anchor = cycle.placeBottom(anchored)
	assert.Equal(t, "s5", before)
	assert.Equal(t, "s6", anchor)
	aheadBefore, aheadAnchor = cycle.placeAheadBottom(anchored)
	assert.Equal(t, "a5", aheadBefore)
	assert.Equal(t, "a6", aheadAnchor)
	assert.Zero(t, anchored.unsaved)

	one := &accountState{account: "acc", top: "s1", bottom: "s1"}
	before, anchor = cycle.placeBottom(one)
	assert.Equal(t, "s1", before)
	assert.Empty(t, anchor, "read from the newest signature, which must be s1")
	assert.Zero(t, one.unsaved)
}
