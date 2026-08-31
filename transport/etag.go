package transport

import (
	"strconv"
	"strings"
)

// receiptETag renders a receipt version as an HTTP entity tag. The version is
// bumped by every mutation, so a changed tag means someone edited the bill.
func receiptETag(version int) string {
	return `"v` + strconv.Itoa(version) + `"`
}

// etagMatches reports whether an If-None-Match header matches the current tag.
// It handles the comma-separated list form, the "*" wildcard, and the W/ weak
// prefix that some proxies and HTTP clients add on their way through.
func etagMatches(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}

	want := normalizeETag(etag)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		if normalizeETag(candidate) == want {
			return true
		}
	}
	return false
}

// normalizeETag strips surrounding whitespace and the weak validator prefix so
// `W/"v3"` and `"v3"` compare equal.
func normalizeETag(tag string) string {
	tag = strings.TrimSpace(tag)
	return strings.TrimPrefix(tag, "W/")
}
