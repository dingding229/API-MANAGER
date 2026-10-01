// Package stack runs the upstream observability services shipped alongside the API.
// These are real upstream executables, not substitutes for their protocols or UIs.
package stack

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed config
var configuration embed.FS

var safePath = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

type Options struct {
	Directory    string
	APIPort      string
	MetricsToken string
	// BinaryDir is primarily for testing; the image installs upstream binaries here.
	BinaryDir string
}

type Stack struct {
	mu        sync.Mutex
	processes []*exec.Cmd
	done      []chan struct{}
	configDir string
	errs      chan error
	once      sync.Once
}

func (s *Stack) Errors() <-chan error { return s.errs }

// Start writes the embedded configuration to a writable data volume, then starts
// the five independent upstream processes. All upstream processes listen on loopback.
func Start(ctx context.Context, options Options) (*Stack, error) {

	if !safePath.MatchString(options.Directory) || strings.Contains(options.Directory, "..") {
		return nil, errors.New("OBSERVABILITY_DIR must be an absolute path with safe characters")
	}
	if _, err := strconv.Atoi(options.APIPort); err != nil {
		return nil, fmt.Errorf("invalid API port: %w", err)
	}
	if options.BinaryDir == "" {
		options.BinaryDir = "/usr/local/bin"
	}
	if !safePath.MatchString(options.BinaryDir) || strings.Contains(options.BinaryDir, "..") {
		return nil, errors.New("observability binary directory must be an absolute safe path")
	}
	for _, name := range []string{"loki", "tempo", "alertmanager", "prometheus", "alloy"} {
		if _, err := os.Stat(filepath.Join(options.BinaryDir, name)); err != nil {
			return nil, fmt.Errorf("%s not installed in image: %w", name, err)
		}
	}
	configDir, err := os.MkdirTemp("", "api-manager-stack-")
	if err != nil {
		return nil, fmt.Errorf("create temporary stack configuration: %w", err)
	}
	cleanupConfig := true
	defer func() {
		if cleanupConfig {
			_ = os.RemoveAll(configDir)
		}
	}()
	for _, name := range []string{"loki", "tempo", "prometheus", "alertmanager", "alloy"} {
		if err := os.MkdirAll(filepath.Join(options.Directory, name), 0700); err != nil {
			return nil, err
		}
	}
	if err := writeConfig(configDir, options); err != nil {
		return nil, err
	}
	if options.MetricsToken != "" {
		if err := os.WriteFile(filepath.Join(configDir, "metrics-token"), []byte(options.MetricsToken), 0600); err != nil {
			return nil, err
		}
	}
	s := &Stack{errs: make(chan error, 5), configDir: configDir}
	cleanupConfig = false
	type service struct {
		name string
		args []string
		env  []string
	}
	services := []service{
		{"loki", []string{"-config.file=" + filepath.Join(configDir, "loki.yaml")}, nil},
		{"tempo", []string{"-config.file=" + filepath.Join(configDir, "tempo.yaml")}, nil},
		{"alertmanager", []string{"--config.file=" + filepath.Join(configDir, "alertmanager.yml"), "--storage.path=" + filepath.Join(options.Directory, "alertmanager"), "--web.listen-address=127.0.0.1:9093", "--cluster.listen-address="}, nil},
		{"prometheus", []string{"--config.file=" + filepath.Join(configDir, "prometheus.yml"), "--storage.tsdb.path=" + filepath.Join(options.Directory, "prometheus"), "--web.listen-address=127.0.0.1:9090"}, nil},
		{"alloy", []string{"run", filepath.Join(configDir, "alloy.alloy"), "--storage.path=" + filepath.Join(options.Directory, "alloy"), "--server.http.listen-addr=127.0.0.1:12345"}, nil},
	}
	for _, item := range services {
		// #nosec G204 -- executable names are from the fixed service table and BinaryDir is validated above.
		cmd := exec.Command(filepath.Join(options.BinaryDir, item.name), item.args...)
		cmd.Env = append(os.Environ(), item.env...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			s.Close()
			return nil, fmt.Errorf("start %s: %w", item.name, err)
		}
		done := make(chan struct{})
		s.mu.Lock()
		s.processes = append(s.processes, cmd)
		s.done = append(s.done, done)
		s.mu.Unlock()
		go func(name string, process *exec.Cmd, stopped chan struct{}) {
			err := process.Wait()
			close(stopped)
			if ctx.Err() == nil {
				s.errs <- fmt.Errorf("%s exited unexpectedly: %v", name, err)
			}
		}(item.name, cmd, done)
	}
	go func() { <-ctx.Done(); s.Close() }()
	return s, nil
}

func (s *Stack) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		processes, done := append([]*exec.Cmd(nil), s.processes...), append([]chan struct{}(nil), s.done...)
		s.mu.Unlock()
		for _, cmd := range processes {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for i, cmd := range processes {
			select {
			case <-done[i]:
			case <-deadline.Done():
				_ = cmd.Process.Kill()
				<-done[i]
			}
		}
		_ = os.RemoveAll(s.configDir)
	})
}

func writeConfig(dir string, options Options) error {
	return fs.WalkDir(configuration, "config", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := configuration.ReadFile(name)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(content), "__DATA_DIR__", options.Directory)
		text = strings.ReplaceAll(text, "__CONFIG_DIR__", dir)
		text = strings.ReplaceAll(text, "__API_PORT__", options.APIPort)
		if name == "config/prometheus.yml" {
			auth := ""
			if options.MetricsToken != "" {
				auth = "bearer_token_file: " + filepath.Join(dir, "metrics-token")
			}
			text = strings.ReplaceAll(text, "__METRICS_AUTH__", auth)
		}
		target := filepath.Join(dir, strings.TrimPrefix(name, "config/"))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return os.WriteFile(target, []byte(text), 0600)
	})
}
