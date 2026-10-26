package connector

import (
	"encoding/json"
	"testing"
)

func TestLinkMetadata(t *testing.T) {
	payload := json.RawMessage(`[{"$class":"LPLinkMetadata","values":[{"title":"A post","summary":"Some text","image":{"URL":"x"}}]}]`)
	title, description, siteName := linkMetadata(payload)
	if title != "A post" || description != "Some text" || siteName != "" {
		t.Errorf("unexpected metadata %q %q %q", title, description, siteName)
	}
	if title, _, _ := linkMetadata(nil); title != "" {
		t.Errorf("expected nothing for empty payload, got %q", title)
	}
}

func TestFileNameForMime(t *testing.T) {
	cases := map[[2]string]string{
		{"IMG_1.HEIC", "image/jpeg"}: "IMG_1.jpg",
		{"IMG_1.jpg", "image/jpeg"}:  "IMG_1.jpg",
		{"preview", "image/png"}:     "preview.png",
		{"", "application/pdf"}:      "attachment.pdf",
		{"notes.txt", ""}:            "notes.txt",
	}
	for in, want := range cases {
		if got := fileNameForMime(in[0], in[1]); got != want {
			t.Errorf("fileNameForMime(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestParseAssociatedGUID(t *testing.T) {
	if guid, part := parseAssociatedGUID("p:1/ABC"); guid != "ABC" || part != 1 {
		t.Errorf("got %q %d", guid, part)
	}
	if guid, part := parseAssociatedGUID("bp:ABC"); guid != "ABC" || part != 0 {
		t.Errorf("got %q %d", guid, part)
	}
	if parseThreadOriginatorPart("2:0:17") != 2 {
		t.Error("expected part 2")
	}
}
