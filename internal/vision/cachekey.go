package vision

import (
	"crypto/md5"
	"fmt"
	"strings"
)

// CacheKey derives the vision_reports cache key for an ORDERED group of image
// content md5s. It is the single source of truth shared by the adapt pre-stage
// (agent.visionThemeReport) and the stamp-mode read-only analysis endpoint, so
// the two paths always resolve the SAME row for the same reference group.
//
// Single image → its raw md5 (aligns with the upload-time prewarm, which caches
// per single-image md5, so a prewarmed single-image report is reused here too).
// Group of 2+ → md5("group:" + comma-joined ordered md5s), order-sensitive.
func CacheKey(md5s []string) string {
	if len(md5s) == 1 {
		return md5s[0]
	}
	return fmt.Sprintf("%x", md5.Sum([]byte("group:"+strings.Join(md5s, ","))))
}
