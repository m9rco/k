package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotenv reads a .env file and sets any keys that are not already present
// in the process environment. Real environment variables always win, so the
// file is a convenience for local development, never an override.
//
// A missing file is not an error: production deployments are expected to inject
// configuration through the environment directly. The format is the usual
// KEY=VALUE per line, with support for:
//   - blank lines and lines starting with '#' (comments)
//   - an optional leading "export " prefix
//   - single- or double-quoted values (quotes are stripped)
//
// Malformed lines are skipped rather than failing the whole load.
func LoadDotenv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		val = unquote(val)

		// Real environment variables take precedence over the file.
		if _, present := os.LookupEnv(key); present {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return sc.Err()
}

// unquote strips a single matching pair of surrounding single or double quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
