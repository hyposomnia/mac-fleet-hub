package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// BOM and valid UTF-8 are authoritative. Legacy automatic detection is best-effort;
// the UI exposes an explicit override because legacy byte streams are ambiguous.
func decodePreviewText(data []byte, requested string) (string, string, error) {
	name := strings.ToLower(strings.TrimSpace(requested))
	if name == "" || name == "auto" {
		switch {
		case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
			name = "utf-16le"
		case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
			name = "utf-16be"
		case utf8.Valid(data):
			name = "utf-8"
		default:
			name = "gb18030"
		}
	}
	var codec encoding.Encoding
	switch name {
	case "utf-8":
		if !utf8.Valid(data) {
			return "", name, fmt.Errorf("invalid UTF-8")
		}
		return strings.TrimPrefix(string(data), "\ufeff"), name, nil
	case "utf-16le":
		codec = unicode.UTF16(unicode.LittleEndian, unicode.UseBOM)
	case "utf-16be":
		codec = unicode.UTF16(unicode.BigEndian, unicode.UseBOM)
	case "gb18030", "gbk":
		codec = simplifiedchinese.GB18030
	case "big5":
		codec = traditionalchinese.Big5
	case "shift_jis":
		codec = japanese.ShiftJIS
	case "windows-1252":
		codec = charmap.Windows1252
	default:
		return "", name, fmt.Errorf("unsupported encoding")
	}
	isUTF16 := name == "utf-16le" || name == "utf-16be"
	if isUTF16 && !validPreviewUTF16(data, name) {
		return "", name, fmt.Errorf("invalid UTF-16")
	}
	decoded, err := codec.NewDecoder().Bytes(data)
	if err == nil && !isUTF16 && bytes.Contains(decoded, []byte("\ufffd")) {
		// A literal replacement character can be represented by legacy encodings
		// too. Only reject a replacement inserted by the decoder for invalid input.
		encoded, encodeErr := codec.NewEncoder().Bytes(decoded)
		if encodeErr != nil || !bytes.Equal(encoded, data) {
			err = fmt.Errorf("invalid text encoding")
		}
	}
	if err != nil || !utf8.Valid(decoded) {
		return "", name, fmt.Errorf("invalid text encoding")
	}
	return string(decoded), name, nil
}

// Validate source code units before decoding: U+FFFD is valid text, whereas an
// unpaired surrogate or a trailing byte must not be silently replaced.
func validPreviewUTF16(data []byte, name string) bool {
	var order binary.ByteOrder = binary.LittleEndian
	if name == "utf-16be" {
		order = binary.BigEndian
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) {
		order = binary.LittleEndian
		data = data[2:]
	} else if bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		order = binary.BigEndian
		data = data[2:]
	}
	if len(data)%2 != 0 {
		return false
	}
	for i := 0; i < len(data); i += 2 {
		unit := order.Uint16(data[i:])
		if unit >= 0xd800 && unit <= 0xdbff {
			if i+3 >= len(data) {
				return false
			}
			low := order.Uint16(data[i+2:])
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 2
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
	}
	return true
}
