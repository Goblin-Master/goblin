package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unsetenv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestLoadDefaultsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOBLIN_LLM_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("GOBLIN_LLM_MODEL", "deepseek-chat-test")

	cfg, err := Load(LoadOptions{
		ConfigPath: filepath.Join(dir, "missing.yaml"),
		EnvPath:    filepath.Join(dir, "missing.env"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "test-key" {
		t.Fatalf("api key = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "deepseek-chat-test" {
		t.Fatalf("model = %q, env override failed", cfg.LLM.Model)
	}
	if cfg.LLM.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("base url = %q", cfg.LLM.BaseURL)
	}
	if cfg.Agent.MaxSteps != 8 || cfg.Agent.KeepRecent != 12 {
		t.Fatalf("agent defaults = %+v", cfg.Agent)
	}
	if cfg.LLM.Timeout.Seconds() != 90 {
		t.Fatalf("timeout = %v", cfg.LLM.Timeout)
	}
}

func TestLoadDeepSeekAlias(t *testing.T) {
	dir := t.TempDir()
	unsetenv(t, "GOBLIN_LLM_API_KEY")
	t.Setenv("DEEPSEEK_API_KEY", "alias-key")

	cfg, err := Load(LoadOptions{
		ConfigPath: filepath.Join(dir, "missing.yaml"),
		EnvPath:    filepath.Join(dir, "missing.env"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "alias-key" {
		t.Fatalf("api key = %q, want alias-key", cfg.LLM.APIKey)
	}
}

func TestLoadConfigFileAndEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
llm:
  model: from-file
  api_key: file-key
agent:
  max_steps: 3
mysql:
  dsn: "goblin:goblin_dev@tcp(127.0.0.1:3306)/goblin?parseTime=true"
logging:
  dir: `+filepath.ToSlash(filepath.Join(dir, "logs"))+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("GOBLIN_LLM_MODEL=from-dotenv\nDEEPSEEK_API_KEY=dotenv-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetenv(t, "GOBLIN_LLM_API_KEY")
	unsetenv(t, "DEEPSEEK_API_KEY")
	unsetenv(t, "GOBLIN_LLM_MODEL")

	cfg, err := Load(LoadOptions{ConfigPath: configPath, EnvPath: envPath})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.Model != "from-dotenv" {
		t.Fatalf("model = %q, .env should beat config file", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "dotenv-key" {
		t.Fatalf("api key = %q", cfg.LLM.APIKey)
	}
	if cfg.Agent.MaxSteps != 3 {
		t.Fatalf("max steps = %d", cfg.Agent.MaxSteps)
	}
}

func TestLoadEnvFileDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GOBLIN_TEST_KEY=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBLIN_TEST_KEY", "from-env")
	if _, err := LoadEnvFile(path, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GOBLIN_TEST_KEY"); got != "from-env" {
		t.Fatalf("value = %q, existing env was overwritten", got)
	}
}

func TestLoadEnvFileSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"# comment",
		"",
		"export GOBLIN_A=one",
		"GOBLIN_B = \"two words\" ",
		"GOBLIN_C='three'",
		"INVALID LINE",
		"1BAD=x",
		"GOBLIN_D=a=b",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"GOBLIN_A", "GOBLIN_B", "GOBLIN_C", "GOBLIN_D"} {
		os.Unsetenv(k)
	}
	if _, err := LoadEnvFile(path, true); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"GOBLIN_A": "one",
		"GOBLIN_B": "two words",
		"GOBLIN_C": "three",
		"GOBLIN_D": "a=b",
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestValidateRejectsMissingKey(t *testing.T) {
	cfg := defaultConfigForTest()
	cfg.LLM.APIKey = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("error = %v, want missing key", err)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"base url", func(c *Config) { c.LLM.BaseURL = "ftp://x" }, "base_url"},
		{"steps", func(c *Config) { c.Agent.MaxSteps = 0 }, "max_steps"},
		{"budget", func(c *Config) { c.Agent.MaxContextTokens = 1000; c.Agent.ReserveOutputToken = 1000 }, "max_context_tokens"},
		{"ratio", func(c *Config) { c.Agent.CompactRatio = 1 }, "compact_ratio"},
		{"keep recent", func(c *Config) { c.Agent.KeepRecent = 2 }, "keep_recent"},
		{"mysql dsn", func(c *Config) { c.MySQL.DSN = " " }, "mysql.dsn"},
		{"logging dir", func(c *Config) { c.Logging.Dir = " " }, "logging.dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfigForTest()
			tc.change(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func defaultConfigForTest() Config {
	return Config{
		LLM: LLMConfig{
			BaseURL:     "https://api.deepseek.com",
			Model:       "deepseek-chat",
			APIKey:      "test-key",
			Timeout:     90 * time.Second,
			MaxRetries:  2,
			Temperature: 0.2,
		},
		Agent: AgentConfig{
			MaxSteps:           8,
			ToolTimeout:        10 * time.Second,
			MaxContextTokens:   32000,
			ReserveOutputToken: 2048,
			CompactRatio:       0.75,
			KeepRecent:         12,
			SummaryMaxTokens:   800,
		},
		MySQL: MySQLConfig{
			DSN: "goblin:goblin_dev@tcp(127.0.0.1:3306)/goblin?parseTime=true",
		},
		Logging: LoggingConfig{
			Level: "info",
			Dir:   "./data/logs",
		},
	}
}
