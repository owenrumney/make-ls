// Package position converts between internal UTF-8 byte offsets and the
// position encoding negotiated with the LSP client.
//
// The parser produces ranges using Go byte indices (len(), regex byte
// offsets). LSP clients may request positions in UTF-16 code units (the
// spec default) or UTF-32 code points. This package converts at the LSP
// boundary so internal code can stay byte-oriented.
package position

import (
	"strings"
	"unicode/utf8"

	"github.com/owenrumney/go-lsp/lsp"
)

// Encoder converts positions and ranges between internal byte offsets and
// the negotiated wire encoding for a specific document.
type Encoder struct {
	Encoding lsp.PositionEncodingKind
	lines    []string
}

// New creates an Encoder for the given document text and wire encoding.
func New(text string, enc lsp.PositionEncodingKind) *Encoder {
	return &Encoder{Encoding: enc, lines: strings.Split(text, "\n")}
}

// ToWire converts an internal byte-offset Position to wire encoding.
func (e *Encoder) ToWire(pos lsp.Position) lsp.Position {
	return lsp.Position{Line: pos.Line, Character: e.byteToWire(pos.Line, pos.Character)}
}

// FromWire converts a wire-encoding Position from the client to a byte offset.
func (e *Encoder) FromWire(pos lsp.Position) lsp.Position {
	return lsp.Position{Line: pos.Line, Character: e.wireToByte(pos.Line, pos.Character)}
}

// RangeToWire converts an internal byte-offset Range to wire encoding.
func (e *Encoder) RangeToWire(r lsp.Range) lsp.Range {
	return lsp.Range{Start: e.ToWire(r.Start), End: e.ToWire(r.End)}
}

func (e *Encoder) byteToWire(line, byteCol int) int {
	if e.Encoding == lsp.PositionEncodingUTF8 || e.Encoding == "" {
		return byteCol
	}
	if line < 0 || line >= len(e.lines) {
		return byteCol
	}
	s := e.lines[line]
	if byteCol <= 0 {
		return 0
	}
	if byteCol > len(s) {
		byteCol = len(s)
	}
	switch e.Encoding {
	case lsp.PositionEncodingUTF16:
		return utf16Units(s[:byteCol])
	case lsp.PositionEncodingUTF32:
		return utf8.RuneCountInString(s[:byteCol])
	default:
		return byteCol
	}
}

func (e *Encoder) wireToByte(line, wireCol int) int {
	if e.Encoding == lsp.PositionEncodingUTF8 || e.Encoding == "" {
		return wireCol
	}
	if line < 0 || line >= len(e.lines) {
		return wireCol
	}
	s := e.lines[line]
	if wireCol <= 0 {
		return 0
	}
	switch e.Encoding {
	case lsp.PositionEncodingUTF16:
		n := 0
		for i, r := range s {
			if n >= wireCol {
				return i
			}
			if r > 0xFFFF {
				n += 2
			} else {
				n++
			}
		}
		return len(s)
	case lsp.PositionEncodingUTF32:
		n := 0
		for i := range s {
			if n >= wireCol {
				return i
			}
			n++
		}
		return len(s)
	default:
		if wireCol > len(s) {
			return len(s)
		}
		return wireCol
	}
}

func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
