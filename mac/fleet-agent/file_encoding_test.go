package main

import "testing"

func TestPreviewUTF16PreservesLiteralReplacementCharacter(t *testing.T) {
	for _, tc := range []struct {
		data     []byte
		encoding string
	}{
		{[]byte{0xff, 0xfe, 0xfd, 0xff}, "auto"},
		{[]byte{0xfe, 0xff, 0xff, 0xfd}, "auto"},
		{[]byte{0xfd, 0xff}, "utf-16le"},
	} {
		got, _, err := decodePreviewText(tc.data, tc.encoding)
		if err != nil || got != "\ufffd" {
			t.Fatalf("%x: %q %v", tc.data, got, err)
		}
	}
}
func TestPreviewUTF16RejectsMalformedSurrogates(t *testing.T) {
	for _, data := range [][]byte{{0xff, 0xfe, 0x00, 0xd8}, {0xff, 0xfe, 0x00, 0xdc}, {0xff, 0xfe, 0x00, 0xd8, 0x41, 0x00}, {0xff, 0xfe, 0x41}} {
		if _, _, err := decodePreviewText(data, "auto"); err == nil {
			t.Fatalf("malformed accepted: %x", data)
		}
	}
}
