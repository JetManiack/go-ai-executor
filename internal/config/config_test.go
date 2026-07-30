package config

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Defaults(t *testing.T) {
	// No flags, no env, no yaml
	cfg, err := LoadConfig(context.Background(), nil, "")
	assert.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "data/executor.db", cfg.DbDsn)
	assert.Equal(t, "./scratch", cfg.SandboxDir)
}

func TestConfig_YamlLoading(t *testing.T) {
	yamlContent := `
listen-addr: :9090
db-dsn: "postgres://user:pass@localhost/db"
sandbox-dir: "/tmp/sandbox"
`
	tmpFile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(yamlContent)); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	cfg, err := LoadConfig(context.Background(), nil, tmpFile.Name())
	assert.NoError(t, err)
	assert.Equal(t, ":9090", cfg.ListenAddr)
	assert.Equal(t, "postgres://user:pass@localhost/db", cfg.DbDsn)
	assert.Equal(t, "/tmp/sandbox", cfg.SandboxDir)
}

func TestConfig_EnvOverride(t *testing.T) {
	os.Setenv("LISTEN_ADDR", ":7070")
	defer os.Unsetenv("LISTEN_ADDR")

	cfg, err := LoadConfig(context.Unmarshal(nil), nil, "") // No yaml
	assert.NoError(t, err)
	assert.Equal(t, ":7070", cfg.ListenAddr)
}

func TestConfig_FlagOverride(t *testing.T) {
	// Simulate flags via a map-like structure or similar logic in our loader
	flags := map[string]string{
		"listen-addr": ":6060",
	}

	cfg, err := LoadConfig(context.Background(), flags, "")
	assert.NoError(t, err)
	assert.Equal(t, ":6060", cfg.ListenAddr)
}
