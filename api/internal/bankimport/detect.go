package bankimport

import (
	"errors"
	"strings"
)

// ErrUnrecognisedFormat means the file matches none of the three known bank
// formats and no column mapping was supplied for the generic fallback.
var ErrUnrecognisedFormat = errors.New("bankimport: file format not recognised")

// Detect picks the parser for an uploaded file, so Juan never has to tell
// corebank which bank a file came from for the three formats already known.
//
// data is the whole file — these statements are at most a few hundred KB, so
// reading one fully before parsing is simpler than streaming, and it is what
// lets both AccountHint and Parse run against independent readers over the
// same bytes.
func Detect(filename string, data []byte) (Parser, error) {
	if strings.HasSuffix(strings.ToLower(filename), ".xlsx") {
		return bgAccountParser{}, nil
	}

	// Only decoded as far as ASCII actually requires: the signature check
	// below never touches an accented character, so a UTF-8 peek is safe
	// even though the BAC format underneath is Latin-1.
	head := data
	if len(head) > 2048 {
		head = head[:2048]
	}
	if strings.Contains(string(head), bgCardHeaderSignature) {
		return bgCardParser{}, nil
	}

	if latin1HasPrefix(head, bacHeaderPrefix) {
		return bacAccountParser{}, nil
	}

	return nil, ErrUnrecognisedFormat
}

// latin1HasPrefix reports whether decoding head as Latin-1 starts with
// prefix, without decoding the whole file — used only for the sniff, since
// the real parse happens later against the full bytes.
//
// Compared rune by rune, not byte by byte: prefix is a Go source string
// (UTF-8), so its accented character is two bytes long there but exactly one
// byte in the Latin-1 head being checked — len(prefix) would count the wrong
// thing.
func latin1HasPrefix(head []byte, prefix string) bool {
	prefixRunes := []rune(prefix)
	if len(head) < len(prefixRunes) {
		return false
	}
	runes := make([]rune, len(prefixRunes))
	for i := range prefixRunes {
		runes[i] = rune(head[i])
	}
	return string(runes) == prefix
}
