package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# a comment
export YUNWU_API_KEY="sk-from-file"
CHAT_PRIMARY_MODEL='deepseek-v4-flash'

  # indented comment
EMPTY=
malformed line without equals
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ensure these are unset so the file can populate them.
	for _, k := range []string{"YUNWU_API_KEY", "CHAT_PRIMARY_MODEL", "EMPTY"} {
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range []string{"YUNWU_API_KEY", "CHAT_PRIMARY_MODEL", "EMPTY"} {
			os.Unsetenv(k)
		}
	})

	if err := LoadDotenv(path); err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}
	if got := os.Getenv("YUNWU_API_KEY"); got != "sk-from-file" {
		t.Errorf("YUNWU_API_KEY = %q, want sk-from-file (quotes + export stripped)", got)
	}
	if got := os.Getenv("CHAT_PRIMARY_MODEL"); got != "deepseek-v4-flash" {
		t.Errorf("CHAT_PRIMARY_MODEL = %q, want deepseek-v4-flash", got)
	}
	if got := os.Getenv("EMPTY"); got != "" {
		t.Errorf("EMPTY = %q, want empty", got)
	}
}

func TestLoadDotenvRealEnvWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("YUNWU_API_KEY=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YUNWU_API_KEY", "from-env")

	if err := LoadDotenv(path); err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}
	if got := os.Getenv("YUNWU_API_KEY"); got != "from-env" {
		t.Errorf("real env should win: got %q, want from-env", got)
	}
}

func TestLoadDotenvMissingFileOK(t *testing.T) {
	if err := LoadDotenv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing .env should not error, got %v", err)
	}
}
