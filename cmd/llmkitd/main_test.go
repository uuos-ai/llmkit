package main

import (
	"testing"

	"github.com/uuos-ai/llmkit/runtimeconfig"
)

func TestParseFlagsProducesExplicitOverridesOnly(t *testing.T) {
	path, overrides, err := parseFlags([]string{"--config", "llmkit.yaml", "--mode", "local-service", "--instance-id", "worker-2", "--max-frame-bytes", "4096", "--admin-listen", ":9443"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "llmkit.yaml" || overrides.Mode == nil || *overrides.Mode != runtimeconfig.ModeLocalService || overrides.InstanceID == nil || *overrides.InstanceID != "worker-2" || overrides.MaxFrameBytes == nil || *overrides.MaxFrameBytes != 4096 {
		t.Fatalf("path=%q overrides=%#v", path, overrides)
	}
	if overrides.Socket != nil {
		t.Fatal("unset CLI value must not override environment or file")
	}
	if overrides.AdminListen == nil || *overrides.AdminListen != ":9443" {
		t.Fatal("admin listen override missing")
	}
}
