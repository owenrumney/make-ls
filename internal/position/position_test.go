package position

import (
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/assert"
)

func TestEncoderUTF8Passthrough(t *testing.T) {
	// Line with multi-byte chars: "é€😀x" — bytes: 2 + 3 + 4 + 1 = 10
	enc := New("é€😀x\n", lsp.PositionEncodingUTF8)

	cases := []struct{ byteCol int }{{0}, {2}, {5}, {9}, {10}}
	for _, c := range cases {
		assert.Equal(t, c.byteCol, enc.ToWire(lsp.Position{Line: 0, Character: c.byteCol}).Character)
		assert.Equal(t, c.byteCol, enc.FromWire(lsp.Position{Line: 0, Character: c.byteCol}).Character)
	}
}

func TestEncoderUTF16(t *testing.T) {
	// "é€😀x"
	//  é = U+00E9   → 2 bytes UTF-8, 1 UTF-16 unit
	//  € = U+20AC   → 3 bytes UTF-8, 1 UTF-16 unit
	//  😀 = U+1F600 → 4 bytes UTF-8, 2 UTF-16 units (surrogate pair)
	//  x           → 1 byte,  1 UTF-16 unit
	enc := New("é€😀x", lsp.PositionEncodingUTF16)

	t.Run("byte to wire", func(t *testing.T) {
		cases := []struct {
			byteCol  int
			wireCol  int
			describe string
		}{
			{0, 0, "start"},
			{2, 1, "after é"},
			{5, 2, "after €"},
			{9, 4, "after 😀 (surrogate pair)"},
			{10, 5, "after x"},
		}
		for _, c := range cases {
			t.Run(c.describe, func(t *testing.T) {
				got := enc.ToWire(lsp.Position{Line: 0, Character: c.byteCol})
				assert.Equal(t, c.wireCol, got.Character)
			})
		}
	})

	t.Run("wire to byte", func(t *testing.T) {
		cases := []struct {
			wireCol  int
			byteCol  int
			describe string
		}{
			{0, 0, "start"},
			{1, 2, "after é"},
			{2, 5, "after €"},
			{4, 9, "after 😀"},
			{5, 10, "after x"},
		}
		for _, c := range cases {
			t.Run(c.describe, func(t *testing.T) {
				got := enc.FromWire(lsp.Position{Line: 0, Character: c.wireCol})
				assert.Equal(t, c.byteCol, got.Character)
			})
		}
	})

	t.Run("round trip", func(t *testing.T) {
		for byteCol := 0; byteCol <= 10; byteCol++ {
			// Only round-trip on rune boundaries: 0, 2, 5, 9, 10.
			if byteCol != 0 && byteCol != 2 && byteCol != 5 && byteCol != 9 && byteCol != 10 {
				continue
			}
			wire := enc.ToWire(lsp.Position{Line: 0, Character: byteCol})
			back := enc.FromWire(wire)
			assert.Equal(t, byteCol, back.Character, "byteCol=%d", byteCol)
		}
	})
}

func TestEncoderUTF32(t *testing.T) {
	// "é€😀x" → 4 runes
	enc := New("é€😀x", lsp.PositionEncodingUTF32)

	cases := []struct {
		byteCol  int
		wireCol  int
		describe string
	}{
		{0, 0, "start"},
		{2, 1, "after é"},
		{5, 2, "after €"},
		{9, 3, "after 😀"},
		{10, 4, "after x"},
	}
	for _, c := range cases {
		t.Run(c.describe, func(t *testing.T) {
			got := enc.ToWire(lsp.Position{Line: 0, Character: c.byteCol})
			assert.Equal(t, c.wireCol, got.Character)

			back := enc.FromWire(got)
			assert.Equal(t, c.byteCol, back.Character)
		})
	}
}

func TestEncoderASCII(t *testing.T) {
	// ASCII: all encodings give identical offsets.
	for _, kind := range []lsp.PositionEncodingKind{
		lsp.PositionEncodingUTF8,
		lsp.PositionEncodingUTF16,
		lsp.PositionEncodingUTF32,
	} {
		t.Run(string(kind), func(t *testing.T) {
			enc := New("hello world", kind)
			for col := 0; col <= 11; col++ {
				got := enc.ToWire(lsp.Position{Line: 0, Character: col})
				assert.Equal(t, col, got.Character)
				back := enc.FromWire(got)
				assert.Equal(t, col, back.Character)
			}
		})
	}
}

func TestEncoderMultiLine(t *testing.T) {
	// Line 0: ASCII, Line 1: contains €
	enc := New("hello\nfoo€bar", lsp.PositionEncodingUTF16)

	// Line 1: "foo€bar" — bytes 0..2 = "foo", 3..5 = "€" (3 bytes), 6..8 = "bar"
	// Wire:                          0..2 = "foo", 3       = "€" (1 unit), 4..6 = "bar"
	got := enc.ToWire(lsp.Position{Line: 1, Character: 6})
	assert.Equal(t, 4, got.Character)

	back := enc.FromWire(got)
	assert.Equal(t, 6, back.Character)
}

func TestEncoderRangeToWire(t *testing.T) {
	enc := New("foo€bar", lsp.PositionEncodingUTF16)
	r := lsp.Range{
		Start: lsp.Position{Line: 0, Character: 3}, // start of €
		End:   lsp.Position{Line: 0, Character: 6}, // after €
	}
	got := enc.RangeToWire(r)
	assert.Equal(t, 3, got.Start.Character)
	assert.Equal(t, 4, got.End.Character)
}

func TestEncoderOutOfRange(t *testing.T) {
	enc := New("abc", lsp.PositionEncodingUTF16)

	// Line past the end → return position unchanged.
	pos := lsp.Position{Line: 99, Character: 5}
	assert.Equal(t, pos, enc.ToWire(pos))
	assert.Equal(t, pos, enc.FromWire(pos))

	// Negative column clamps to 0.
	assert.Equal(t, 0, enc.ToWire(lsp.Position{Line: 0, Character: -1}).Character)
	assert.Equal(t, 0, enc.FromWire(lsp.Position{Line: 0, Character: -1}).Character)

	// Byte column past end clamps to line length.
	assert.Equal(t, 3, enc.ToWire(lsp.Position{Line: 0, Character: 99}).Character)
}

func TestEncoderEmptyEncodingDefaultsToPassthrough(t *testing.T) {
	enc := New("foo€bar", "")
	pos := lsp.Position{Line: 0, Character: 6}
	assert.Equal(t, pos, enc.ToWire(pos))
	assert.Equal(t, pos, enc.FromWire(pos))
}
