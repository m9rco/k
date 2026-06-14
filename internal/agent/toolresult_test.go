package agent

import "testing"

func TestTaskIDFromResponse(t *testing.T) {
	tests := []struct {
		name string
		resp string
		want string
	}{
		{"edit_image result", `{"task_id":"task_abc","status":"queued","note":"x"}`, "task_abc"},
		{"video result", `{"task_id":"task_v1","status":"queued"}`, "task_v1"},
		{"crop array result (no task)", `[{"asset_id":"a1","size_id":"s1"}]`, ""},
		{"empty task id", `{"task_id":"","status":"done"}`, ""},
		{"malformed json", `not json`, ""},
		{"truncated json", `{"task_id":"task_`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskIDFromResponse(tt.resp); got != tt.want {
				t.Fatalf("taskIDFromResponse(%q) = %q, want %q", tt.resp, got, tt.want)
			}
		})
	}
}

func TestToolKind(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"edit_image", "generate"},
		{"image_to_video", "video"},
		{"crawl_game_assets", "crawl"},
		{"search_images", "search"},
		{"crop_to_sizes", ""},
		{"list_platform_sizes", ""},
		{"unknown_tool", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolKind(tt.name); got != tt.want {
				t.Fatalf("toolKind(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
