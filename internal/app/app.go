package app

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"github.com/Goblin-Master/goblin/internal/agent"
	"github.com/Goblin-Master/goblin/internal/config"
	"github.com/Goblin-Master/goblin/internal/db"
	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/logging"
	"github.com/Goblin-Master/goblin/internal/memory"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
)

type Options struct {
	ConfigPath string
	EnvPath    string
	Verbose    bool
}

// App is the composition root. MySQL is opened once here and injected into the
// session and todo stores. Any initialization failure is returned before the
// CLI command runs.
type App struct {
	Config  config.Config
	Conn    *sql.DB
	Store   *session.MySQLStore
	Engine  *agent.Engine
	LogFile *logging.File
	Verbose bool
}

func New(ctx context.Context, opts Options) (*App, error) {
	cfg, err := config.Load(config.LoadOptions{ConfigPath: opts.ConfigPath, EnvPath: opts.EnvPath})
	if err != nil {
		return nil, err
	}
	conn, err := db.Open(ctx, cfg.MySQL.DSN)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = conn.Close() }

	store, err := session.NewMySQLStore(conn)
	if err != nil {
		cleanup()
		return nil, err
	}
	todoStore, err := tools.NewMySQLTodoStore(conn)
	if err != nil {
		cleanup()
		return nil, err
	}
	root, err := os.Getwd()
	if err != nil {
		cleanup()
		return nil, err
	}
	readDocs, err := tools.NewReadDocs(root, 64*1024)
	if err != nil {
		cleanup()
		return nil, err
	}
	registry, err := tools.NewRegistryWithTimeout(cfg.Agent.ToolTimeout,
		tools.NewCalculator(), tools.NewSearch(), readDocs, tools.NewTodo(todoStore))
	if err != nil {
		cleanup()
		return nil, err
	}
	client, err := llm.NewDeepSeek(llm.Options{
		BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model,
		Timeout: cfg.LLM.Timeout, MaxRetries: cfg.LLM.MaxRetries, Temperature: cfg.LLM.Temperature,
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	memoryManager, err := memory.New(memory.Options{
		Client: client, Model: cfg.LLM.Model,
		MaxContextTokens: cfg.Agent.MaxContextTokens, ReserveOutputTokens: cfg.Agent.ReserveOutputToken,
		KeepRecent: cfg.Agent.KeepRecent, SummaryMaxTokens: cfg.Agent.SummaryMaxTokens,
		CompactRatio: cfg.Agent.CompactRatio,
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	logFile, err := logging.New(cfg.Logging.Dir, cfg.Logging.Level)
	if err != nil {
		cleanup()
		return nil, err
	}
	engine, err := agent.New(agent.Options{
		Client: client, Store: store, Tools: registry, Memory: memoryManager,
		Logger: logFile.Logger, Model: cfg.LLM.Model, MaxSteps: cfg.Agent.MaxSteps,
		Temperature: cfg.LLM.Temperature,
	})
	if err != nil {
		_ = logFile.Close()
		cleanup()
		return nil, err
	}
	return &App{
		Config: cfg, Conn: conn, Store: store, Engine: engine,
		LogFile: logFile, Verbose: opts.Verbose,
	}, nil
}

func (a *App) Close() {
	if a == nil {
		return
	}
	if a.LogFile != nil {
		_ = a.LogFile.Close()
	}
	if a.Conn != nil {
		_ = a.Conn.Close()
	}
}

func (a *App) CreateSession() (string, error) {
	if a == nil || a.Store == nil {
		return "", errors.New("app 未初始化")
	}
	id := session.NewID()
	if _, err := a.Store.Create(id); err != nil {
		return "", err
	}
	return id, nil
}
