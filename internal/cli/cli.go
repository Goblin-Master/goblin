package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/Goblin-Master/goblin/internal/agent"
	"github.com/Goblin-Master/goblin/internal/app"
	"github.com/Goblin-Master/goblin/internal/logging"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/spf13/cobra"
)

// Main builds the Cobra command tree, executes one invocation, and returns an
// exit code. stdout/stderr are injected so command behavior is testable.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	root.SetContext(ctx)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return 1
	}
	return 0
}

type rootOptions struct {
	configPath string
	envPath    string
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:   "goblin",
		Short: "从零实现的最小可用 CLI Agent",
		Long: `Goblin 是一个不依赖 Agent 框架的 CLI Agent。

它使用 DeepSeek 原生 tool_calls 完成工具决策，使用 MySQL 持久化
Session、消息、摘要与待办，并在上下文超预算时自动压缩。`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "配置文件路径，默认 config.yaml")
	root.PersistentFlags().StringVar(&opts.envPath, "env-file", "", "环境文件路径，默认 .env")

	root.AddCommand(
		newChatCommand(opts, stdout),
		newRunCommand(opts, stdout),
		newSessionsCommand(opts, stdout),
	)
	return root
}

func newChatCommand(opts *rootOptions, stdout io.Writer) *cobra.Command {
	var sessionID string
	var newSession bool
	var verbose bool
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "新建或恢复 Session，进入交互模式",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChat(cmd.Context(), opts, sessionID, newSession, verbose, stdout)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "恢复已存在的 UUID 会话")
	cmd.Flags().BoolVar(&newSession, "new", false, "强制新建会话")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "打印 trace_id 与工具摘要")
	return cmd
}

func newRunCommand(opts *rootOptions, stdout io.Writer) *cobra.Command {
	var sessionID string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "run --session <uuid> <问题>",
		Short: "在已存在的 Session 中执行一次输入",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOnce(cmd.Context(), opts, sessionID, strings.Join(args, " "), verbose, stdout)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "已存在的 UUID 会话")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "打印 trace_id 与工具摘要")
	return cmd
}

func newSessionsCommand(opts *rootOptions, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "列出所有 Session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSessions(opts, stdout)
		},
	}
}

// runner holds a fully initialized App. Composition (config, MySQL, tools,
// model, logging) happens in internal/app before this type is used.
type runner struct {
	*app.App
}

func runChat(ctx context.Context, opts *rootOptions, sessionID string, newSession, verbose bool, stdout io.Writer) error {
	if sessionID != "" && newSession {
		return errors.New("--session 与 --new 不能同时使用")
	}
	application, err := app.New(ctx, app.Options{
		ConfigPath: opts.configPath, EnvPath: opts.envPath, Verbose: verbose,
	})
	if err != nil {
		return err
	}
	defer application.Close()
	r := &runner{App: application}

	var id string
	if sessionID != "" {
		id, err = session.ParseID(sessionID)
		if err != nil {
			return err
		}
		if _, err := r.Store.Load(id); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("会话 %s 不存在；用 goblin sessions 查看，或运行 goblin chat 新建", id)
			}
			return err
		}
	} else {
		id, err = r.CreateSession()
		if err != nil {
			return err
		}
	}
	return r.repl(ctx, id, stdout)
}

func (r *runner) repl(ctx context.Context, initialID string, out io.Writer) error {
	id := initialID
	fmt.Fprintf(out, "Goblin | model=%s | tools=%s\n", r.Config.LLM.Model, strings.Join(r.toolsNames(), ", "))
	fmt.Fprintf(out, "Session: %s\n输入 /help 查看命令，/quit 退出。\n", id)
	reader := bufio.NewReader(os.Stdin)
	var last agent.Reply
	for {
		fmt.Fprint(out, "\n你> ")
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(out)
				return nil
			}
			return err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if strings.HasPrefix(input, "/") {
			quit, newID, err := r.command(ctx, id, input, out, last)
			if err != nil {
				fmt.Fprintln(out, "错误:", err)
				continue
			}
			if quit {
				return nil
			}
			if newID != "" {
				id = newID
				last = agent.Reply{}
			}
			continue
		}
		reply, err := r.Engine.Run(ctx, id, input)
		if err != nil {
			fmt.Fprintln(out, "错误:", err)
			continue
		}
		last = reply
		fmt.Fprintf(out, "\nAgent> %s\n", reply.Text)
		if r.Verbose {
			fmt.Fprintf(out, "trace_id=%s\n", reply.TraceID)
			r.printTools(out, reply.ToolCalls)
		}
	}
}

func (r *runner) toolsNames() []string {
	return []string{"calculator", "search", "read_docs", "todo"}
}

func (r *runner) printTools(out io.Writer, calls []agent.ToolCallRecord) {
	for _, call := range calls {
		status := "ok"
		if call.IsError {
			status = "error"
		}
		fmt.Fprintf(out, "  → %s(%s) %dms %s\n", call.Name, logging.Preview(call.Arguments, 80), call.DurationMS, status)
	}
}

func (r *runner) command(ctx context.Context, id, input string, out io.Writer, last agent.Reply) (bool, string, error) {
	fields := strings.Fields(input)
	switch fields[0] {
	case "/quit", "/exit":
		return true, "", nil
	case "/help":
		fmt.Fprintln(out, "/help  /new  /sessions  /switch <uuid>  /history [n]  /compact  /last  /quit")
	case "/new":
		newID, err := r.CreateSession()
		if err != nil {
			return false, "", err
		}
		fmt.Fprintf(out, "已切换新会话: %s\n", newID)
		return false, newID, nil
	case "/sessions":
		return false, "", r.listSessions(out)
	case "/switch":
		if len(fields) != 2 {
			return false, "", errors.New("用法: /switch <uuid>")
		}
		newID, err := session.ParseID(fields[1])
		if err != nil {
			return false, "", err
		}
		if _, err := r.Store.Load(newID); err != nil {
			return false, "", err
		}
		fmt.Fprintf(out, "已切换会话: %s\n", newID)
		return false, newID, nil
	case "/history":
		n := 10
		if len(fields) == 2 {
			_, _ = fmt.Sscanf(fields[1], "%d", &n)
		}
		sess, err := r.Store.Load(id)
		if err != nil {
			return false, "", err
		}
		start := len(sess.Messages) - n
		if start < 0 {
			start = 0
		}
		for _, msg := range sess.Messages[start:] {
			fmt.Fprintf(out, "[%s] %s\n", msg.Role, logging.Preview(msg.Content, 300))
		}
	case "/compact":
		compacted, err := r.Engine.Compact(ctx, id)
		if err != nil {
			return false, "", err
		}
		if compacted {
			fmt.Fprintln(out, "已压缩上下文。")
		} else {
			fmt.Fprintln(out, "当前上下文不需要压缩。")
		}
	case "/last":
		if last.TraceID == "" {
			fmt.Fprintln(out, "还没有运行记录。")
			break
		}
		fmt.Fprintf(out, "trace_id=%s\n", last.TraceID)
		r.printTools(out, last.ToolCalls)
		if len(last.ToolCalls) == 0 {
			fmt.Fprintln(out, "上次运行没有工具调用。")
		}
	default:
		return false, "", fmt.Errorf("未知命令 %s", fields[0])
	}
	return false, "", nil
}

func (r *runner) listSessions(out io.Writer) error {
	metas, err := r.Store.List()
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		fmt.Fprintln(out, "暂无会话。")
		return nil
	}
	for _, meta := range metas {
		fmt.Fprintf(out, "%s  %s  msgs=%d  %s\n",
			meta.ID, meta.UpdatedAt.Format("2006-01-02 15:04"), meta.Messages, meta.Title)
	}
	return nil
}

func runOnce(ctx context.Context, opts *rootOptions, sessionID, input string, verbose bool, stdout io.Writer) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("run 需要 --session <uuid>")
	}
	if strings.TrimSpace(input) == "" {
		return errors.New("run 需要一个问题")
	}
	application, err := app.New(ctx, app.Options{
		ConfigPath: opts.configPath, EnvPath: opts.envPath, Verbose: verbose,
	})
	if err != nil {
		return err
	}
	defer application.Close()
	reply, err := application.Engine.Run(ctx, sessionID, input)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, reply.Text)
	if verbose {
		fmt.Fprintf(stdout, "trace_id=%s\n", reply.TraceID)
		r := &runner{App: application}
		r.printTools(stdout, reply.ToolCalls)
	}
	return nil
}

func runSessions(opts *rootOptions, stdout io.Writer) error {
	application, err := app.New(context.Background(), app.Options{
		ConfigPath: opts.configPath, EnvPath: opts.envPath,
	})
	if err != nil {
		return err
	}
	defer application.Close()
	r := &runner{App: application}
	return r.listSessions(stdout)
}
