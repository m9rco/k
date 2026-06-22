package vision

import (
	"crypto/md5"
	"fmt"
	"strings"
	"testing"
)

// Single image must return the bare md5 unchanged — this is the contract that
// keeps it aligned with the upload prewarm (which caches per raw md5). Any
// wrapping here would silently miss every prewarmed single-image report.
func TestCacheKeySingleIsBareMD5(t *testing.T) {
	if got := CacheKey([]string{"abc"}); got != "abc" {
		t.Fatalf("single-image key = %q, want %q", got, "abc")
	}
}

// Group key is md5("group:"+ordered join) and is order-sensitive.
func TestCacheKeyGroupHashAndOrder(t *testing.T) {
	want := fmt.Sprintf("%x", md5.Sum([]byte("group:"+strings.Join([]string{"a", "b"}, ","))))
	if got := CacheKey([]string{"a", "b"}); got != want {
		t.Fatalf("group key = %q, want %q", got, want)
	}
	if CacheKey([]string{"a", "b"}) == CacheKey([]string{"b", "a"}) {
		t.Fatal("group key must be order-sensitive")
	}
}

// Pure function: repeated calls on the same input are stable.
func TestCacheKeyStable(t *testing.T) {
	in := []string{"x", "y", "z"}
	if CacheKey(in) != CacheKey(in) {
		t.Fatal("CacheKey must be deterministic")
	}
}
