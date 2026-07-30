// Package runtimeconfig defines strict, non-secret llmkitd startup settings.
package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Mode string

const (
	ModeSidecar      Mode = "sidecar"
	ModeLocalService Mode = "local-service"
	ModeGateway      Mode = "gateway"
)

type CustomProviderSync string

const (
	SyncDisabled CustomProviderSync = "disabled"
	SyncManaged  CustomProviderSync = "managed"
)

type TLSConfig struct {
	CertificateFile string `json:"certificate_file" yaml:"certificate_file"`
	PrivateKeyFile  string `json:"private_key_file" yaml:"private_key_file"`
}

type StorageConfig struct {
	ConfigStore       string `json:"config_store" yaml:"config_store"`
	SecretStore       string `json:"secret_store" yaml:"secret_store"`
	AuditStore        string `json:"audit_store" yaml:"audit_store"`
	CoordinationStore string `json:"coordination_store" yaml:"coordination_store"`
	SQLitePath        string `json:"sqlite_path" yaml:"sqlite_path"`
}

type Config struct {
	Mode                     Mode               `json:"mode" yaml:"mode"`
	InstanceID               string             `json:"instance_id" yaml:"instance_id"`
	Socket                   string             `json:"socket" yaml:"socket"`
	Listen                   string             `json:"listen" yaml:"listen"`
	AdminListen              string             `json:"admin_listen" yaml:"admin_listen"`
	ParentPID                int                `json:"parent_pid" yaml:"parent_pid"`
	ClientTokenHashFile      string             `json:"client_token_hash_file" yaml:"client_token_hash_file"`
	BusinessTargetsURL       string             `json:"business_targets_url" yaml:"business_targets_url"`
	BusinessServiceTokenFile string             `json:"business_service_token_file" yaml:"business_service_token_file"`
	CustomProviderSync       CustomProviderSync `json:"custom_provider_sync" yaml:"custom_provider_sync"`
	MaxFrameBytes            uint32             `json:"max_frame_bytes" yaml:"max_frame_bytes"`
	TLS                      TLSConfig          `json:"tls" yaml:"tls"`
	Storage                  StorageConfig      `json:"storage" yaml:"storage"`
}

type Overrides struct {
	Mode                     *Mode
	InstanceID               *string
	Socket                   *string
	Listen                   *string
	AdminListen              *string
	ParentPID                *int
	ClientTokenHashFile      *string
	BusinessTargetsURL       *string
	BusinessServiceTokenFile *string
	CustomProviderSync       *CustomProviderSync
	MaxFrameBytes            *uint32
	TLSCertificateFile       *string
	TLSPrivateKeyFile        *string
	ConfigStore              *string
	SecretStore              *string
	AuditStore               *string
	CoordinationStore        *string
	SQLitePath               *string
}

type LookupEnv func(string) (string, bool)

var instancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func Defaults() Config {
	return Config{
		Mode: ModeSidecar, InstanceID: "default",
		CustomProviderSync: SyncDisabled,
		MaxFrameBytes:      8 << 20,
	}
}

func Load(path string, lookup LookupEnv, overrides Overrides) (Config, error) {
	config := Defaults()
	if path != "" {
		if err := decodeFile(path, &config); err != nil {
			return Config{}, err
		}
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if err := applyEnvironment(&config, lookup); err != nil {
		return Config{}, err
	}
	applyOverrides(&config, overrides)
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if !instancePattern.MatchString(c.InstanceID) {
		return errors.New("llmkitd config: instance_id must match [A-Za-z0-9][A-Za-z0-9_-]{0,63}")
	}
	if c.MaxFrameBytes == 0 {
		return errors.New("llmkitd config: max_frame_bytes must be positive")
	}
	if c.CustomProviderSync != SyncDisabled && c.CustomProviderSync != SyncManaged {
		return fmt.Errorf("llmkitd config: unsupported custom_provider_sync %q", c.CustomProviderSync)
	}
	switch c.Mode {
	case ModeSidecar:
		if c.Socket == "" || c.ParentPID <= 0 {
			return errors.New("llmkitd config: sidecar requires socket and parent_pid")
		}
	case ModeLocalService:
		if c.Socket == "" || c.ClientTokenHashFile == "" {
			return errors.New("llmkitd config: local-service requires socket and client_token_hash_file")
		}
		if c.Storage.CoordinationStore != "" && (c.BusinessServiceTokenFile == "" || c.TLS.CertificateFile == "" || c.TLS.PrivateKeyFile == "") {
			return errors.New("llmkitd config: shared local-service binding storage requires business_service_token_file and mTLS certificate/private key")
		}
		if c.Storage.SQLitePath != "" && c.CustomProviderSync != SyncManaged {
			return errors.New("llmkitd config: local SQLite/OS secret storage requires custom_provider_sync=managed")
		}
		if c.CustomProviderSync == SyncManaged && c.BusinessTargetsURL == "" && c.Storage.SQLitePath == "" {
			return errors.New("llmkitd config: managed local-service requires business_targets_url or storage.sqlite_path")
		}
	case ModeGateway:
		if c.Listen == "" || c.AdminListen == "" || c.ClientTokenHashFile == "" || c.BusinessServiceTokenFile == "" {
			return errors.New("llmkitd config: gateway requires distinct data/admin listen addresses, client_token_hash_file, and business_service_token_file")
		}
		if c.Listen == c.AdminListen {
			return errors.New("llmkitd config: gateway data and admin listen addresses must differ")
		}
		if c.TLS.CertificateFile == "" || c.TLS.PrivateKeyFile == "" {
			return errors.New("llmkitd config: gateway requires a TLS certificate and private key")
		}
		if c.CustomProviderSync != SyncManaged {
			return errors.New("llmkitd config: gateway requires custom_provider_sync=managed")
		}
		if c.Storage.ConfigStore == "" || c.Storage.SecretStore == "" || c.Storage.AuditStore == "" || c.Storage.CoordinationStore == "" {
			return errors.New("llmkitd config: gateway requires config, secret, audit, and coordination stores")
		}
	default:
		return fmt.Errorf("llmkitd config: unsupported mode %q", c.Mode)
	}
	return nil
}

func decodeFile(path string, destination *Config) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("llmkitd config: open: %w", err)
	}
	defer file.Close()
	var decoder interface{ Decode(any) error }
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		jsonDecoder := json.NewDecoder(file)
		jsonDecoder.DisallowUnknownFields()
		decoder = jsonDecoder
	} else {
		yamlDecoder := yaml.NewDecoder(file)
		yamlDecoder.KnownFields(true)
		decoder = yamlDecoder
	}
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("llmkitd config: decode: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("llmkitd config: exactly one document is required")
	}
	return nil
}

func applyEnvironment(config *Config, lookup LookupEnv) error {
	setString := func(name string, destination *string) {
		if value, ok := lookup(name); ok {
			*destination = value
		}
	}
	if value, ok := lookup("LLMKIT_MODE"); ok {
		config.Mode = Mode(value)
	}
	setString("LLMKIT_INSTANCE_ID", &config.InstanceID)
	setString("LLMKIT_SOCKET", &config.Socket)
	setString("LLMKIT_LISTEN", &config.Listen)
	setString("LLMKIT_ADMIN_LISTEN", &config.AdminListen)
	setString("LLMKIT_CLIENT_TOKEN_HASH_FILE", &config.ClientTokenHashFile)
	setString("LLMKIT_BUSINESS_TARGETS_URL", &config.BusinessTargetsURL)
	setString("LLMKIT_BUSINESS_SERVICE_TOKEN_FILE", &config.BusinessServiceTokenFile)
	setString("LLMKIT_TLS_CERTIFICATE_FILE", &config.TLS.CertificateFile)
	setString("LLMKIT_TLS_PRIVATE_KEY_FILE", &config.TLS.PrivateKeyFile)
	setString("LLMKIT_CONFIG_STORE", &config.Storage.ConfigStore)
	setString("LLMKIT_SECRET_STORE", &config.Storage.SecretStore)
	setString("LLMKIT_AUDIT_STORE", &config.Storage.AuditStore)
	setString("LLMKIT_COORDINATION_STORE", &config.Storage.CoordinationStore)
	setString("LLMKIT_SQLITE_PATH", &config.Storage.SQLitePath)
	if value, ok := lookup("LLMKIT_CUSTOM_PROVIDER_SYNC"); ok {
		config.CustomProviderSync = CustomProviderSync(value)
	}
	if value, ok := lookup("LLMKIT_PARENT_PID"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("llmkitd config: LLMKIT_PARENT_PID must be an integer")
		}
		config.ParentPID = parsed
	}
	if value, ok := lookup("LLMKIT_MAX_FRAME_BYTES"); ok {
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return errors.New("llmkitd config: LLMKIT_MAX_FRAME_BYTES must be an unsigned integer")
		}
		config.MaxFrameBytes = uint32(parsed)
	}
	return nil
}

func applyOverrides(config *Config, overrides Overrides) {
	if overrides.Mode != nil {
		config.Mode = *overrides.Mode
	}
	if overrides.InstanceID != nil {
		config.InstanceID = *overrides.InstanceID
	}
	if overrides.Socket != nil {
		config.Socket = *overrides.Socket
	}
	if overrides.Listen != nil {
		config.Listen = *overrides.Listen
	}
	if overrides.AdminListen != nil {
		config.AdminListen = *overrides.AdminListen
	}
	if overrides.ParentPID != nil {
		config.ParentPID = *overrides.ParentPID
	}
	if overrides.ClientTokenHashFile != nil {
		config.ClientTokenHashFile = *overrides.ClientTokenHashFile
	}
	if overrides.BusinessTargetsURL != nil {
		config.BusinessTargetsURL = *overrides.BusinessTargetsURL
	}
	if overrides.BusinessServiceTokenFile != nil {
		config.BusinessServiceTokenFile = *overrides.BusinessServiceTokenFile
	}
	if overrides.CustomProviderSync != nil {
		config.CustomProviderSync = *overrides.CustomProviderSync
	}
	if overrides.MaxFrameBytes != nil {
		config.MaxFrameBytes = *overrides.MaxFrameBytes
	}
	if overrides.TLSCertificateFile != nil {
		config.TLS.CertificateFile = *overrides.TLSCertificateFile
	}
	if overrides.TLSPrivateKeyFile != nil {
		config.TLS.PrivateKeyFile = *overrides.TLSPrivateKeyFile
	}
	if overrides.ConfigStore != nil {
		config.Storage.ConfigStore = *overrides.ConfigStore
	}
	if overrides.SecretStore != nil {
		config.Storage.SecretStore = *overrides.SecretStore
	}
	if overrides.AuditStore != nil {
		config.Storage.AuditStore = *overrides.AuditStore
	}
	if overrides.CoordinationStore != nil {
		config.Storage.CoordinationStore = *overrides.CoordinationStore
	}
	if overrides.SQLitePath != nil {
		config.Storage.SQLitePath = *overrides.SQLitePath
	}
}
