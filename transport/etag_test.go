package transport

import "testing"

func TestReceiptETag(t *testing.T) {
	if got, want := receiptETag(1), `"v1"`; got != want {
		t.Errorf("receiptETag(1) = %q, want %q", got, want)
	}
	if got, want := receiptETag(42), `"v42"`; got != want {
		t.Errorf("receiptETag(42) = %q, want %q", got, want)
	}
}

func TestETagMatches(t *testing.T) {
	tests := []struct {
		name        string
		ifNoneMatch string
		etag        string
		want        bool
	}{
		{"empty header never matches", "", `"v3"`, false},
		{"exact match", `"v3"`, `"v3"`, true},
		{"different version", `"v2"`, `"v3"`, false},
		{"wildcard matches", "*", `"v3"`, true},
		{"wildcard with whitespace", " * ", `"v3"`, true},
		{"weak validator prefix", `W/"v3"`, `"v3"`, true},
		{"list containing the tag", `"v1", "v3", "v7"`, `"v3"`, true},
		{"list without the tag", `"v1", "v2"`, `"v3"`, false},
		{"list with weak entries", `W/"v1", W/"v3"`, `"v3"`, true},
		{"unquoted value does not match", "v3", `"v3"`, false},
		// "v30" must not satisfy a request holding "v3".
		{"prefix is not a match", `"v3"`, `"v30"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := etagMatches(tt.ifNoneMatch, tt.etag); got != tt.want {
				t.Errorf("etagMatches(%q, %q) = %v, want %v", tt.ifNoneMatch, tt.etag, got, tt.want)
			}
		})
	}
}
