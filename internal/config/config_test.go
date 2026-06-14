package config

import "testing"

func TestValidateRelayAllowsDebugDumpDisabled(t *testing.T) {
	cfg := validRelayConfig()
	cfg.DebugDump.Enabled = false
	cfg.DebugDump.Mode = "local"

	if err := ValidateRelay(cfg); err != nil {
		t.Fatalf("ValidateRelay() error = %v", err)
	}
}

func TestValidateRelayAllowsRemoteDebugDump(t *testing.T) {
	cfg := validRelayConfig()
	cfg.DebugDump.Enabled = true
	cfg.DebugDump.Mode = "remote"

	if err := ValidateRelay(cfg); err != nil {
		t.Fatalf("ValidateRelay() error = %v", err)
	}
}

func TestValidateRelayRejectsLocalDebugDump(t *testing.T) {
	cfg := validRelayConfig()
	cfg.DebugDump.Enabled = true
	cfg.DebugDump.Mode = "local"
	cfg.DebugDump.Dir = "/tmp/uapi-debug-dumps"

	if err := ValidateRelay(cfg); err == nil {
		t.Fatal("ValidateRelay() expected error for local debug dump")
	}
}

func validRelayConfig() *Config {
	return &Config{
		Gateway: GatewayConfig{
			RequireInternal: true,
			ControlURL:      "http://gateway:8080",
			RelayNodeID:     "123e4567-e89b-12d3-a456-426614174000",
		},
	}
}
