package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the fully merged application configuration.
type Config struct {
	LLM     LLMConfig     `mapstructure:"llm"`
	Agent   AgentConfig   `mapstructure:"agent"`
	MySQL   MySQLConfig   `mapstructure:"mysql"`
	Logging LoggingConfig `mapstructure:"logging"`
}

type LLMConfig struct {
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	APIKey      string        `mapstructure:"api_key"`
	Timeout     time.Duration `mapstructure:"timeout"`
	MaxRetries  int           `mapstructure:"max_retries"`
	Temperature float64       `mapstructure:"temperature"`
}

type AgentConfig struct {
	MaxSteps           int           `mapstructure:"max_steps"`
	ToolTimeout        time.Duration `mapstructure:"tool_timeout"`
	MaxContextTokens   int           `mapstructure:"max_context_tokens"`
	ReserveOutputToken int           `mapstructure:"reserve_output_tokens"`
	CompactRatio       float64       `mapstructure:"compact_ratio"`
	KeepRecent         int           `mapstructure:"keep_recent"`
	SummaryMaxTokens   int           `mapstructure:"summary_max_tokens"`
}

type MySQLConfig struct {
	DSN string `mapstructure:"dsn"`
}

type LoggingConfig struct {
	Level   string `mapstructure:"level"`
	Dir     string `mapstructure:"dir"`
	Verbose bool   `mapstructure:"verbose"`
}

// LoadOptions controls configuration discovery. Empty paths use defaults:
// config.yaml and .env in the current directory. Missing files are ignored.
type LoadOptions struct {
	ConfigPath string
	EnvPath    string
}

// Load merges defaults, .env, config.yaml, environment variables and validates
// the result. Precedence: process env > .env > config file > defaults.
func Load(opts LoadOptions) (Config, error) {
	envPath := opts.EnvPath
	if envPath == "" {
		envPath = ".env"
	}
	if _, err := os.Stat(envPath); err == nil {
		if _, err := LoadEnvFile(envPath, false); err != nil {
			return Config{}, fmt.Errorf("load env file %s: %w", envPath, err)
		}
	} else if opts.EnvPath != "" && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("stat env file %s: %w", envPath, err)
	}

	v := viper.New()
	v.SetDefault("llm.base_url", "https://api.deepseek.com")
	v.SetDefault("llm.model", "deepseek-chat")
	v.SetDefault("llm.api_key", "")
	v.SetDefault("llm.timeout", "90s")
	v.SetDefault("llm.max_retries", 2)
	v.SetDefault("llm.temperature", 0.2)
	v.SetDefault("agent.max_steps", 8)
	v.SetDefault("agent.tool_timeout", "10s")
	v.SetDefault("agent.max_context_tokens", 32000)
	v.SetDefault("agent.reserve_output_tokens", 2048)
	v.SetDefault("agent.compact_ratio", 0.75)
	v.SetDefault("agent.keep_recent", 12)
	v.SetDefault("agent.summary_max_tokens", 800)
	v.SetDefault("mysql.dsn", "goblin:123456@tcp(127.0.0.1:3306)/goblin?parseTime=true&charset=utf8mb4&loc=Local")
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.dir", "./data/logs")
	v.SetDefault("logging.verbose", false)

	v.SetEnvPrefix("GOBLIN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.BindEnv("llm.api_key", "GOBLIN_LLM_API_KEY", "DEEPSEEK_API_KEY"); err != nil {
		return Config{}, fmt.Errorf("bind api key env: %w", err)
	}

	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = "config.yaml"
	}
	if _, err := os.Stat(configPath); err == nil {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", configPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("stat config %s: %w", configPath, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	// Viper's env binding handles GOBLIN_LLM_API_KEY; DEEPSEEK_API_KEY is a
	// documented alias, kept explicit so a config-file value does not win.
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects unsafe or unusable combinations early.
func (c Config) Validate() error {
	if strings.TrimSpace(c.LLM.APIKey) == "" {
		return errors.New("缺少 DeepSeek API Key：请在 .env 中填写 DEEPSEEK_API_KEY，或设置同名环境变量")
	}
	u, err := url.Parse(c.LLM.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("llm.base_url 必须是 http(s) URL，当前为 %q", c.LLM.BaseURL)
	}
	if strings.TrimSpace(c.LLM.Model) == "" {
		return errors.New("llm.model 不能为空")
	}
	if c.LLM.Timeout <= 0 {
		return errors.New("llm.timeout 必须大于 0")
	}
	if c.LLM.MaxRetries < 0 {
		return errors.New("llm.max_retries 不能为负数")
	}
	if c.LLM.Temperature < 0 || c.LLM.Temperature > 2 {
		return errors.New("llm.temperature 必须在 [0,2] 内")
	}
	if c.Agent.MaxSteps < 1 {
		return errors.New("agent.max_steps 必须至少为 1")
	}
	if c.Agent.ToolTimeout <= 0 {
		return errors.New("agent.tool_timeout 必须大于 0")
	}
	if c.Agent.MaxContextTokens <= c.Agent.ReserveOutputToken {
		return errors.New("agent.max_context_tokens 必须大于 reserve_output_tokens")
	}
	if c.Agent.CompactRatio <= 0 || c.Agent.CompactRatio >= 1 {
		return errors.New("agent.compact_ratio 必须在 (0,1) 内")
	}
	if c.Agent.KeepRecent < 4 {
		return errors.New("agent.keep_recent 至少为 4")
	}
	if c.Agent.SummaryMaxTokens < 1 {
		return errors.New("agent.summary_max_tokens 必须大于 0")
	}
	if strings.TrimSpace(c.MySQL.DSN) == "" {
		return errors.New("mysql.dsn 不能为空")
	}
	if strings.TrimSpace(c.Logging.Dir) == "" {
		return errors.New("logging.dir 不能为空")
	}
	return nil
}

// InputBudget is the number of tokens available for the prompt.
func (c AgentConfig) InputBudget() int {
	return c.MaxContextTokens - c.ReserveOutputToken
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadEnvFile loads a small KEY=VALUE file without executing anything.
// Existing process environment variables are preserved unless overwrite is true.
func LoadEnvFile(path string, overwrite bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	loaded := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !envKeyPattern.MatchString(key) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if !overwrite {
			if _, exists := os.LookupEnv(key); exists {
				continue
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return loaded, err
		}
		loaded++
	}
	if err := scanner.Err(); err != nil {
		return loaded, err
	}
	return loaded, nil
}
