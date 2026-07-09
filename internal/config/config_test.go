package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\n" +
		"TEST_DOTENV_PLAIN=hello\n" +
		"TEST_DOTENV_URI=mongodb+srv://u:p@host/?retryWrites=true&w=majority\n" +
		"TEST_DOTENV_QUOTED=\"quoted value\"\n" +
		"TEST_DOTENV_PRESET=from-file\n" +
		"\n" +
		"not-a-kv-line\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TEST_DOTENV_PLAIN", "TEST_DOTENV_URI", "TEST_DOTENV_QUOTED"} {
		os.Unsetenv(k)
		defer os.Unsetenv(k)
	}
	t.Setenv("TEST_DOTENV_PRESET", "from-env")

	LoadDotEnv(path)

	if got := os.Getenv("TEST_DOTENV_PLAIN"); got != "hello" {
		t.Errorf("PLAIN = %q, want hello", got)
	}
	// & and ? in values (Mongo SRV URIs) must survive verbatim.
	if got := os.Getenv("TEST_DOTENV_URI"); got != "mongodb+srv://u:p@host/?retryWrites=true&w=majority" {
		t.Errorf("URI = %q", got)
	}
	if got := os.Getenv("TEST_DOTENV_QUOTED"); got != "quoted value" {
		t.Errorf("QUOTED = %q, want unquoted", got)
	}
	// Real environment always wins over the file.
	if got := os.Getenv("TEST_DOTENV_PRESET"); got != "from-env" {
		t.Errorf("PRESET = %q, want from-env", got)
	}
}

func TestLoadDotEnv_MissingFile(t *testing.T) {
	LoadDotEnv(filepath.Join(t.TempDir(), "no-such-file")) // must not panic
}
