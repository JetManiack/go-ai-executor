package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/urfave/cli/v3"

	"go-ai-executor/internal/frontend"
	"go-ai-executor/internal/humanauth"
	"go-ai-executor/internal/mcpserver"
	"go-ai-executor/internal/restapi"
	"go-ai-executor/internal/sandbox"
	"go-ai-executor/internal/storage"
)

func newRootCommand() *cli.Command {
	return &cli.Command{
		Name:  "go-ai-executor",
		Usage: "AI Executor MCP Server with Multi-Agent Sandboxes, Web UI, OIDC Auth & Live Terminal Streaming",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "listen-addr",
				Value:   ":8080",
				Usage:   "HTTP server listen address",
				Sources: cli.EnvVars("LISTEN_ADDR"),
			},
			&cli.StringFlag{
				Name:    "db-dsn",
				Value:   "data/executor.db",
				Usage:   "Database DSN (SQLite file path or postgres:// connection string)",
				Sources: cli.EnvVars("DB_DSN"),
			},
			&cli.StringFlag{
				Name:    "sandbox-dir",
				Value:   "./scratch",
				Usage:   "Root directory serving as base for agent sandboxes",
				Sources: cli.EnvVars("SANDBOX_DIR"),
			},
			&cli.StringFlag{
				Name:    "transport",
				Value:   "http",
				Usage:   "MCP transport: 'http' (default, with Web UI) or 'stdio'",
				Sources: cli.EnvVars("TRANSPORT"),
			},
			&cli.BoolFlag{
				Name:    "auth-stub",
				Value:   true,
				Usage:   "Use fixed admin identity for human auth (local dev mode)",
				Sources: cli.EnvVars("AUTH_STUB"),
			},
			&cli.IntFlag{
				Name:    "default-timeout-sec",
				Value:   30,
				Usage:   "Default command execution timeout in seconds",
				Sources: cli.EnvVars("DEFAULT_TIMEOUT_SEC"),
			},
			&cli.IntFlag{
				Name:    "max-output-kb",
				Value:   512,
				Usage:   "Maximum stdout/stderr size in kilobytes",
				Sources: cli.EnvVars("MAX_OUTPUT_KB"),
			},
			&cli.StringFlag{
				Name:    "shell",
				Value:   "/bin/sh",
				Usage:   "Shell binary path for command execution",
				Sources: cli.EnvVars("SHELL_PATH"),
			},
			&cli.BoolFlag{
				Name:    "devel",
				Usage:   "Dev mode: serve static files directly from disk instead of embedded snapshot",
				Sources: cli.EnvVars("DEVEL"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			dsn := cmd.String("db-dsn")
			sandboxDir := cmd.String("sandbox-dir")
			transport := cmd.String("transport")
			listenAddr := cmd.String("listen-addr")
			authStub := cmd.Bool("auth-stub")
			devel := cmd.Bool("devel")

			db, err := storage.Open(dsn)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}

			mgr, err := sandbox.NewManager(sandbox.Config{
				RootDir:        sandboxDir,
				DefaultTimeout: time.Duration(cmd.Int("default-timeout-sec")) * time.Second,
				MaxOutputBytes: int(cmd.Int("max-output-kb") * 1024),
				Shell:          cmd.String("shell"),
			})
			if err != nil {
				return fmt.Errorf("failed to initialize sandbox manager: %w", err)
			}

			if transport == "stdio" {
				slog.Info("starting MCP server on stdio transport...")
				return mcpserver.ServeStdio(ctx, mgr, db)
			}

			var authProvider humanauth.Provider
			if authStub {
				stub, err := humanauth.NewStubProvider(db)
				if err != nil {
					return fmt.Errorf("failed to initialize auth stub: %w", err)
				}
				authProvider = stub
			} else {
				return fmt.Errorf("OIDC Keycloak integration requires OIDC issuer flags")
			}

			// Main HTTP Router
			mainRouter := chi.NewRouter()

			// 1. MCP Agent Transport (/mcp)
			mcpHandler := mcpserver.NewHTTPHandler(mgr, db)
			mainRouter.Mount("/mcp", mcpHandler)

			// 2. REST API (/api/*)
			apiRouter := restapi.NewRouter(restapi.RouterOptions{
				DB:          db,
				Manager:     mgr,
				AuthProvider: authProvider,
			})
			mainRouter.Mount("/api", apiRouter)

			// 3. Static Web UI (/)
			frontendFS, err := frontend.FS(devel)
			if err != nil {
				return fmt.Errorf("failed to load frontend assets: %w", err)
			}
			fileServer := http.FileServer(frontendFS)
			mainRouter.Handle("/*", fileServer)

			srv := &http.Server{
				Addr:    listenAddr,
				Handler: mainRouter,
			}

			go func() {
				slog.Info("🚀 Server listening", "addr", listenAddr, "web_ui", fmt.Sprintf("http://localhost%s", listenAddr), "mcp_endpoint", fmt.Sprintf("http://localhost%s/mcp", listenAddr))
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("http server error", "error", err)
				}
			}()

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

			select {
			case <-sigChan:
				slog.Info("shutting down server...")
			case <-ctx.Done():
				slog.Info("context cancelled, shutting down...")
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		},
	}
}

func main() {
	cmd := newRootCommand()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		slog.Error("application exited with error", "error", err)
		os.Exit(1)
	}
}
