package runtimeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLayering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llmkit.yaml")
	if err := os.WriteFile(path, []byte("mode: local-service\ninstance_id: from-file\nsocket: /tmp/file.sock\nclient_token_hash_file: /tmp/tokens.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"LLMKIT_INSTANCE_ID": "from-env", "LLMKIT_SOCKET": "/tmp/env.sock"}
	mode := ModeSidecar
	parentPID := 42
	config, err := Load(path, func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}, Overrides{Mode: &mode, ParentPID: &parentPID})
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeSidecar || config.InstanceID != "from-env" || config.Socket != "/tmp/env.sock" {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadRejectsUnknownAndUnsafeGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llmkit.json")
	if err := os.WriteFile(path, []byte(`{"mode":"sidecar","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, func(string) (string, bool) { return "", false }, Overrides{}); err == nil {
		t.Fatal("expected unknown field error")
	}
	config := Defaults()
	config.Mode = ModeGateway
	config.InstanceID = "gateway-1"
	config.Listen = ":8443"
	config.AdminListen = ":9443"
	config.ClientTokenHashFile = "/tokens"
	config.BusinessServiceTokenFile = "/service-token"
	config.CustomProviderSync = SyncManaged
	if err := config.Validate(); err == nil {
		t.Fatal("expected gateway without TLS and stores to fail closed")
	}
}

func TestGatewayRequiresDistinctAdminListener(t *testing.T) {
	config := Defaults()
	config.Mode = ModeGateway
	config.Listen = ":8443"
	config.AdminListen = ":8443"
	config.ClientTokenHashFile = "/tokens"
	config.BusinessServiceTokenFile = "/service-token"
	config.CustomProviderSync = SyncManaged
	config.TLS.CertificateFile = "/cert"
	config.TLS.PrivateKeyFile = "/key"
	config.Storage.ConfigStore = "https://config"
	config.Storage.SecretStore = "https://secret"
	config.Storage.AuditStore = "https://audit"
	config.Storage.CoordinationStore = "https://coordination"
	if err := config.Validate(); err == nil {
		t.Fatal("same data/control listener accepted")
	}
	config.AdminListen = ":9443"
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceIDValidation(t *testing.T) {
	config := Defaults()
	config.InstanceID = "../escape"
	config.Socket = "/tmp/socket"
	config.ParentPID = 1
	if err := config.Validate(); err == nil {
		t.Fatal("expected unsafe instance ID to be rejected")
	}
}

func TestLocalSQLiteRequiresManagedSync(t *testing.T) {
	config := Defaults()
	config.Mode = ModeLocalService
	config.Socket = "/tmp/llmkit.sock"
	config.ClientTokenHashFile = "/tmp/tokens.json"
	config.Storage.SQLitePath = "/tmp/llmkit.sqlite"
	if err := config.Validate(); err == nil {
		t.Fatal("expected disabled-sync SQLite to fail")
	}
	config.CustomProviderSync = SyncManaged
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
