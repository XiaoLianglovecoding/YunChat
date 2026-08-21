package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultIsValidAfterSecretIsSet(t *testing.T) {
	cfg := Default()
	cfg.JWT.Secret = "01234567890123456789012345678901"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid default config: %v", err)
	}
}

func TestValidateRejectsUnsafeValues(t *testing.T) {
	cfg := Default()
	cfg.JWT.Secret = "short"
	cfg.ID.WorkerID = 1024

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := Load("../../configs/config.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.Server.Port != 18080 || cfg.Server.ReadTimeout <= 0 {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
}

func TestOpenAPIIsValidYAML(t *testing.T) {
	content, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI document: %v", err)
	}
	var document struct {
		OpenAPI string         `yaml:"openapi"`
		Paths   map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}
	if document.OpenAPI == "" || len(document.Paths) == 0 {
		t.Fatalf("OpenAPI document is incomplete: version=%q paths=%d", document.OpenAPI, len(document.Paths))
	}
}
