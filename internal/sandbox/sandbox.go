package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrPathOutsideSandbox = errors.New("path is outside sandbox directory")
	ErrCommandTimeout     = errors.New("command execution timed out")
)

type Config struct {
	RootDir        string
	DefaultTimeout time.Duration
	MaxOutputBytes int
	Shell          string
	AllowedEnvs    []string
}

type Sandbox struct {
	config Config
	mu     sync.RWMutex
}

type ExecResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
}

type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

type SandboxStatus struct {
	RootDir        string   `json:"root_dir"`
	DefaultTimeout string   `json:"default_timeout"`
	MaxOutputBytes int      `json:"max_output_bytes"`
	Shell          string   `json:"shell"`
	AllowedEnvs    []string `json:"allowed_envs"`
}

func DefaultConfig(rootDir string) Config {
	if rootDir == "" {
		rootDir = "./scratch"
	}
	return Config{
		RootDir:        rootDir,
		DefaultTimeout: 30 * time.Second,
		MaxOutputBytes: 512 * 1024, // 512 KB
		Shell:          "/bin/sh",
		AllowedEnvs: []string{
			"PATH=/usr/bin:/bin:/usr/local/bin:/usr/sbin:/sbin",
			"LANG=C.UTF-8",
			"LC_ALL=C.UTF-8",
		},
	}
}

func New(cfg Config) (*Sandbox, error) {
	if cfg.RootDir == "" {
		return nil, fmt.Errorf("root directory cannot be empty")
	}

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for root directory: %w", err)
	}

	if err := os.MkdirAll(absRoot, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sandbox root directory: %w", err)
	}

	// Resolve symlinks on root dir to prevent symlink traversal trickery
	evalRoot, err := filepath.EvalSymlinks(absRoot)
	if err == nil {
		absRoot = evalRoot
	}

	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 30 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 512 * 1024
	}
	if cfg.Shell == "" {
		cfg.Shell = "/bin/sh"
	}

	cfg.RootDir = absRoot
	return &Sandbox{config: cfg}, nil
}

func (s *Sandbox) ResolvePath(relPath string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cleaned := filepath.Clean(relPath)
	if cleaned == "." || cleaned == "" {
		return s.config.RootDir, nil
	}

	var target string
	if filepath.IsAbs(cleaned) {
		// If an absolute path is passed, verify if it falls inside s.config.RootDir
		target = cleaned
	} else {
		target = filepath.Join(s.config.RootDir, cleaned)
	}

	rel, err := filepath.Rel(s.config.RootDir, target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPathOutsideSandbox, err)
	}

	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideSandbox, relPath)
	}

	return target, nil
}

func (s *Sandbox) ExecCommand(ctx context.Context, command string, timeout time.Duration, workDir string) (ExecResult, error) {
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	if timeout <= 0 {
		timeout = cfg.DefaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var targetWorkDir string
	if workDir != "" {
		resolved, err := s.ResolvePath(workDir)
		if err != nil {
			return ExecResult{}, fmt.Errorf("invalid working directory: %w", err)
		}
		targetWorkDir = resolved
	} else {
		targetWorkDir = cfg.RootDir
	}

	cmd := exec.CommandContext(execCtx, cfg.Shell, "-c", command)
	cmd.Dir = targetWorkDir

	// Setup clean environment
	env := append([]string{}, cfg.AllowedEnvs...)
	env = append(env, fmt.Sprintf("HOME=%s", cfg.RootDir))
	env = append(env, fmt.Sprintf("PWD=%s", targetWorkDir))
	cmd.Env = env

	stdoutBuf := &limitedBuffer{limit: cfg.MaxOutputBytes}
	stderrBuf := &limitedBuffer{limit: cfg.MaxOutputBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	startTime := time.Now()
	err := cmd.Run()
	durationMs := time.Since(startTime).Milliseconds()

	exitCode := 0
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return ExecResult{
				Stdout:     stdoutBuf.String(),
				Stderr:     stderrBuf.String() + "\n[command timed out]",
				ExitCode:   -1,
				DurationMs: durationMs,
				Truncated:  stdoutBuf.truncated || stderrBuf.truncated,
			}, ErrCommandTimeout
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return ExecResult{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		ExitCode:   exitCode,
		DurationMs: durationMs,
		Truncated:  stdoutBuf.truncated || stderrBuf.truncated,
	}, nil
}

func (s *Sandbox) ReadFile(relPath string) ([]byte, error) {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(absPath)
}

func (s *Sandbox) WriteFile(relPath string, data []byte, perm os.FileMode) error {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return err
	}
	if perm == 0 {
		perm = 0644
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}
	return os.WriteFile(absPath, data, perm)
}

func (s *Sandbox) ListDir(relPath string) ([]FileInfo, error) {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	var results []FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryRelPath, err := filepath.Rel(s.config.RootDir, filepath.Join(absPath, entry.Name()))
		if err != nil {
			entryRelPath = entry.Name()
		}

		results = append(results, FileInfo{
			Name:    entry.Name(),
			Path:    entryRelPath,
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return results, nil
}

func (s *Sandbox) DeleteFile(relPath string) error {
	absPath, err := s.ResolvePath(relPath)
	if err != nil {
		return err
	}

	if absPath == s.config.RootDir {
		return fmt.Errorf("cannot delete sandbox root directory")
	}

	return os.RemoveAll(absPath)
}

func (s *Sandbox) GetStatus() SandboxStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SandboxStatus{
		RootDir:        s.config.RootDir,
		DefaultTimeout: s.config.DefaultTimeout.String(),
		MaxOutputBytes: s.config.MaxOutputBytes,
		Shell:          s.config.Shell,
		AllowedEnvs:    s.config.AllowedEnvs,
	}
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (n int, err error) {
	if b.buf.Len() >= b.limit {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		b.truncated = true
		n, err = b.buf.Write(p[:remaining])
		if err != nil {
			return n, err
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}
