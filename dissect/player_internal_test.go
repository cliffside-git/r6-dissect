package dissect

import (
	"bytes"
	"testing"
)

// The bug this guards: build 9734089 (a Y11S2 mid-season patch) swapped the
// player-id marker, and 9785623 -- still reporting itself as Y11S2 -- swapped
// it back. A `CodeVersion >= 9734089` gate was correct for exactly one build
// and wrong for every build after it, which failed ~100% of R6 parses for
// eleven weeks. Version numbers do not tell you which marker a build uses;
// only the bytes do.
func TestPlayerIDIndicatorIsDetectedNotGuessed(t *testing.T) {
	filler := bytes.Repeat([]byte{0x11}, 64)
	buf := func(marker []byte) []byte {
		return append(append(append([]byte{}, filler...), marker...), filler...)
	}

	cases := []struct {
		name        string
		codeVersion int
		body        []byte
		want        []byte
	}{
		{"Y11S2 base 9718747 uses the classic marker", 9718747, buf(playerIDClassic), playerIDClassic},
		{"Y11S2 mid-season 9734089 uses its own marker", 9734089, buf(playerIDMidseason), playerIDMidseason},
		{"Y11S2 9785623 reverted to the classic marker", 9785623, buf(playerIDClassic), playerIDClassic},
		{"Y11S3 9879602 still uses the classic marker", 9879602, buf(playerIDClassic), playerIDClassic},
		{"a future build that swaps back again is handled", 99999999, buf(playerIDMidseason), playerIDMidseason},
		{"pre-Y7S2 keeps its own marker regardless of body", Y7S2 - 1, buf(playerIDClassic), playerIDY7S2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &Reader{b: c.body, Header: Header{CodeVersion: c.codeVersion}}
			if got := r.playerIDIndicator(); !bytes.Equal(got, c.want) {
				t.Errorf("playerIDIndicator() = % X, want % X", got, c.want)
			}
		})
	}
}

// An unrecognised build must still answer a usable pattern: readPlayer would
// panic on a nil one, and the seek error it produces instead is what the
// caller already knows how to classify.
func TestPlayerIDIndicatorFallsBackOnAnUnknownFormat(t *testing.T) {
	r := &Reader{b: bytes.Repeat([]byte{0x11}, 64), Header: Header{CodeVersion: 99999999}}
	if got := r.playerIDIndicator(); !bytes.Equal(got, playerIDClassic) {
		t.Errorf("playerIDIndicator() = % X, want the classic marker as a fallback", got)
	}
}

// The scan is amortised across all ten players, so the answer must stick even
// after Read() releases the buffer.
func TestPlayerIDIndicatorIsCached(t *testing.T) {
	r := &Reader{b: bytes.Repeat([]byte{0x11}, 32), Header: Header{CodeVersion: 9879602}}
	r.idIndicator = playerIDMidseason
	if got := r.playerIDIndicator(); !bytes.Equal(got, playerIDMidseason) {
		t.Errorf("playerIDIndicator() = % X, want the cached value", got)
	}
	r.b = nil
	if got := r.playerIDIndicator(); !bytes.Equal(got, playerIDMidseason) {
		t.Errorf("playerIDIndicator() after the buffer was released = % X, want the cached value", got)
	}
}
