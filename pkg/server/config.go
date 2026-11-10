package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the server's whole runtime configuration.
//
// Every field has a sane zero-value default, because the spec requires that a
// missing *required* value fails loudly while optional ones simply work
// (spec server-runtime / 启动配置).
type Config struct {
	// Listen is the address to bind, e.g. ":8100". Required.
	Listen string `json:"listen"`
	// DataDir holds all persisted state. Required.
	DataDir string `json:"data_dir"`
	// PublicURL is shown to clients as a hint; optional.
	PublicURL string `json:"public_url,omitempty"`

	// TLSCert and TLSKey enable direct TLS when both are set.
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey  string `json:"tls_key,omitempty"`

	// TrustProxy makes the server read X-Forwarded-For. Off by default, because
	// trusting it without a proxy in front lets clients forge their source
	// address (spec server-runtime / 监听与传输加密).
	TrustProxy bool `json:"trust_proxy"`

	// Limits.
	MaxFrameBytes     int64 `json:"max_frame_bytes"`
	MaxMessageBytes   int   `json:"max_message_bytes"`
	RequestsPerSecond int   `json:"requests_per_second"`
	HeartbeatSeconds  int   `json:"heartbeat_seconds"`
	HistoryPageSize   int   `json:"history_page_size"`
	SendQueueDepth    int   `json:"send_queue_depth"`

	// Auth.
	LoginAttemptsPerMinute int `json:"login_attempts_per_minute"`
	RegisterPerMinute      int `json:"register_per_minute"`

	// ShutdownGrace is how long in-flight work gets to finish.
	ShutdownGraceSeconds int `json:"shutdown_grace_seconds"`

	// LogLevel is one of debug, info, warn, error.
	LogLevel string `json:"log_level"`

	// Creation requires an invite? Not yet: open registration is the MVP.
	HeartbeatInterval time.Duration `json:"-"`
	ShutdownGrace     time.Duration `json:"-"`
}

// DefaultConfig returns the defaults used to seed a fresh config file.
func DefaultConfig() Config {
	return Config{
		Listen:                 ":8100",
		DataDir:                "./data",
		MaxFrameBytes:          64 << 10,
		MaxMessageBytes:        4000,
		RequestsPerSecond:      20,
		HeartbeatSeconds:       30,
		HistoryPageSize:        50,
		SendQueueDepth:         256,
		LoginAttemptsPerMinute: 5,
		RegisterPerMinute:      5,
		ShutdownGraceSeconds:   10,
		LogLevel:               "info",
	}
}

// LoadConfig reads a config file, applies environment overrides, then validates.
// A missing file is not an error when GenerateIfMissing is set — the caller gets
// defaults and a written template so a first run explains itself.
func LoadConfig(path string, generateIfMissing bool) (Config, error) {
	cfg := DefaultConfig()

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("config: parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		if !generateIfMissing {
			return cfg, fmt.Errorf("config: %s not found", path)
		}
		if err := writeDefaultConfig(path, cfg); err != nil {
			return cfg, err
		}
		fmt.Fprintf(os.Stderr, "wrote default config to %s\n", path)
	default:
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}

	applyEnv(&cfg)
	return cfg, cfg.Validate()
}

// writeDefaultConfig seeds a config file with a helpful header.
//
// The header must stay inside JSON: encoding/json rejects a file that starts with
// "//", which would make the generated config unreadable by the very program that
// wrote it. Comments therefore travel as extra "_comment" fields, which the parser
// ignores and a human reading the file still benefits from.
func writeDefaultConfig(path string, cfg Config) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("config: create %s: %w", dir, err)
		}
	}

	// Marshal an ordered document rather than the struct, so the file reads in a
	// deliberate order with comments next to the fields they explain.
	doc := orderedConfig{
		comment("link_chat server config. Every field has a built-in default; " +
			"only listen and data_dir are required."),
		comment("Environment overrides: LINKCHAT_LISTEN, LINKCHAT_DATA_DIR, " +
			"LINKCHAT_LOG_LEVEL, LINKCHAT_TLS_CERT, LINKCHAT_TLS_KEY, LINKCHAT_TRUST_PROXY."),
		{"listen", cfg.Listen},
		comment("Address to bind. Set tls_cert and tls_key together to serve TLS directly."),
		{"data_dir", cfg.DataDir},
		{"public_url", cfg.PublicURL},
		{"tls_cert", cfg.TLSCert},
		{"tls_key", cfg.TLSKey},
		comment("trust_proxy makes the server read X-Forwarded-For. " +
			"Only enable behind a reverse proxy you control."),
		{"trust_proxy", cfg.TrustProxy},
		comment("Limits."),
		{"max_frame_bytes", cfg.MaxFrameBytes},
		{"max_message_bytes", cfg.MaxMessageBytes},
		{"requests_per_second", cfg.RequestsPerSecond},
		{"heartbeat_seconds", cfg.HeartbeatSeconds},
		{"history_page_size", cfg.HistoryPageSize},
		{"send_queue_depth", cfg.SendQueueDepth},
		comment("Authentication abuse limits, per source address."),
		{"login_attempts_per_minute", cfg.LoginAttemptsPerMinute},
		{"register_per_minute", cfg.RegisterPerMinute},
		comment("How long shutdown waits for in-flight work."),
		{"shutdown_grace_seconds", cfg.ShutdownGraceSeconds},
		comment("One of: debug, info, warn, error."),
		{"log_level", cfg.LogLevel},
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// orderedConfig is a JSON object that marshals its entries in insertion order.
type orderedConfig []configEntry

type configEntry struct {
	key   string
	value any
}

// comment builds an ignored entry that documents the field after it.
func comment(text string) configEntry {
	return configEntry{key: "_comment", value: text}
}

// MarshalJSON emits the entries in order. Duplicate keys are legal to emit and
// harmless here: "_comment" is never read, and real fields appear once.
func (o orderedConfig) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(e.key)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(e.value)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// applyEnv lets an operator override the essentials without editing files,
// which is what container deployments need. Values are logged at startup by
// the caller noting their source (spec server-runtime / 环境变量覆盖配置).
func applyEnv(cfg *Config) {
	if v := os.Getenv("LINKCHAT_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("LINKCHAT_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("LINKCHAT_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("LINKCHAT_TLS_CERT"); v != "" {
		cfg.TLSCert = v
	}
	if v := os.Getenv("LINKCHAT_TLS_KEY"); v != "" {
		cfg.TLSKey = v
	}
	if v := os.Getenv("LINKCHAT_TRUST_PROXY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.TrustProxy = b
		}
	}
}

// Validate checks required fields and normalises derived durations. It returns
// an error naming the offending field, never a bare "invalid config".
func (c *Config) Validate() error {
	var problems []string

	if strings.TrimSpace(c.Listen) == "" {
		problems = append(problems, "listen is required")
	} else if err := checkListen(c.Listen); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.DataDir) == "" {
		problems = append(problems, "data_dir is required")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		problems = append(problems, "tls_cert and tls_key must be set together")
	}
	if c.TLSCert != "" {
		if _, err := os.Stat(c.TLSCert); err != nil {
			problems = append(problems, fmt.Sprintf("tls_cert: %v", err))
		}
		if _, err := os.Stat(c.TLSKey); err != nil {
			problems = append(problems, fmt.Sprintf("tls_key: %v", err))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}

	// Defaults for fields the user may have omitted in an older file.
	if c.MaxFrameBytes <= 0 {
		c.MaxFrameBytes = DefaultConfig().MaxFrameBytes
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = DefaultConfig().MaxMessageBytes
	}
	if c.RequestsPerSecond <= 0 {
		c.RequestsPerSecond = DefaultConfig().RequestsPerSecond
	}
	if c.HeartbeatSeconds <= 0 {
		c.HeartbeatSeconds = DefaultConfig().HeartbeatSeconds
	}
	if c.HistoryPageSize <= 0 {
		c.HistoryPageSize = DefaultConfig().HistoryPageSize
	}
	if c.SendQueueDepth <= 0 {
		c.SendQueueDepth = DefaultConfig().SendQueueDepth
	}
	if c.LoginAttemptsPerMinute <= 0 {
		c.LoginAttemptsPerMinute = DefaultConfig().LoginAttemptsPerMinute
	}
	if c.RegisterPerMinute <= 0 {
		c.RegisterPerMinute = DefaultConfig().RegisterPerMinute
	}
	if c.ShutdownGraceSeconds <= 0 {
		c.ShutdownGraceSeconds = DefaultConfig().ShutdownGraceSeconds
	}
	c.HeartbeatInterval = time.Duration(c.HeartbeatSeconds) * time.Second
	c.ShutdownGrace = time.Duration(c.ShutdownGraceSeconds) * time.Second
	return nil
}

func checkListen(addr string) error {
	// Accept ":8100", "host:port" and "0.0.0.0:8100".
	host, port, ok := strings.Cut(addr, ":")
	if !ok {
		return fmt.Errorf("listen: %q is not host:port", addr)
	}
	_ = host
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("listen: port %q is not in 1..65535", port)
	}
	return nil
}

// EnsureDataDir creates the data directory and verifies writability, so a
// permission problem surfaces at startup rather than on the first message.
func (c *Config) EnsureDataDir() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("data_dir: %w", err)
	}
	probe := filepath.Join(c.DataDir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("data_dir is not writable: %w", err)
	}
	_ = os.Remove(probe)
	return nil
}
