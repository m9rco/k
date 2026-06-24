package generation

import (
	"reflect"
	"strings"
	"testing"
)

// TestPrimaryAndExtras locks the source/reference resolution that decides which
// image is sent to the provider as the base (SourceImage) vs. extra references
// (ReferenceImages). The regression it guards: a non-empty ReferenceAssetIDs
// used to override SourceAssetID, so "把图X放进图Z" dropped the base 图Z and the
// model invented a character on 图X instead of compositing onto 图Z.
func TestPrimaryAndExtras(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		refs        []string
		wantPrimary string
		wantExtras  []string
	}{
		{
			name:        "source plus references: source is primary, refs are extras",
			source:      "Z",
			refs:        []string{"X", "Y"},
			wantPrimary: "Z",
			wantExtras:  []string{"X", "Y"},
		},
		{
			name:        "source only",
			source:      "Z",
			refs:        nil,
			wantPrimary: "Z",
			wantExtras:  nil,
		},
		{
			name:        "references only: first is anchor, rest are extras",
			source:      "",
			refs:        []string{"X", "Y"},
			wantPrimary: "X",
			wantExtras:  []string{"Y"},
		},
		{
			name:        "single reference only",
			source:      "",
			refs:        []string{"X"},
			wantPrimary: "X",
			wantExtras:  nil,
		},
		{
			name:        "no input at all",
			source:      "",
			refs:        nil,
			wantPrimary: "",
			wantExtras:  nil,
		},
		{
			name:        "reference echoes the source id: deduped out of extras",
			source:      "Z",
			refs:        []string{"Z", "X"},
			wantPrimary: "Z",
			wantExtras:  []string{"X"},
		},
		{
			name:        "duplicate extras collapse",
			source:      "Z",
			refs:        []string{"X", "X", "Y"},
			wantPrimary: "Z",
			wantExtras:  []string{"X", "Y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := GenerateParams{SourceAssetID: tt.source, ReferenceAssetIDs: tt.refs}
			gotPrimary, gotExtras := p.primaryAndExtras()
			if gotPrimary != tt.wantPrimary {
				t.Errorf("primary = %q, want %q", gotPrimary, tt.wantPrimary)
			}
			if len(gotExtras) != 0 || len(tt.wantExtras) != 0 {
				if !reflect.DeepEqual(gotExtras, tt.wantExtras) {
					t.Errorf("extras = %v, want %v", gotExtras, tt.wantExtras)
				}
			}
		})
	}
}

// TestPrimaryAndExtrasCap verifies the total image count (primary + extras)
// stays within MaxReferenceImages.
func TestPrimaryAndExtrasCap(t *testing.T) {
	refs := make([]string, MaxReferenceImages+5)
	for i := range refs {
		refs[i] = "ref" + strings.Repeat("x", i+1) // distinct ids so none dedupe
	}
	p := GenerateParams{SourceAssetID: "base", ReferenceAssetIDs: refs}
	primary, extras := p.primaryAndExtras()
	if primary != "base" {
		t.Fatalf("primary = %q, want base", primary)
	}
	if total := 1 + len(extras); total > MaxReferenceImages {
		t.Errorf("total images = %d, exceeds MaxReferenceImages %d", total, MaxReferenceImages)
	}
}
