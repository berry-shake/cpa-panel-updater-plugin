package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHostConfigReadsConfiguredRepository(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.pro.yaml")
	raw := []byte("remote-management:\n  panel-github-repository: https://github.com/acme/panel\n")

	got := ResolveHostConfig(HostConfigEnvironment{
		Args:   []string{"server", "--config", configPath},
		Getwd:  func() (string, error) { return "/unused", nil },
		Getenv: func(string) string { return "" },
		ReadFile: func(path string) ([]byte, error) {
			if path != configPath {
				t.Fatalf("ReadFile(%q), want %q", path, configPath)
			}
			return raw, nil
		},
		Stat: os.Stat,
	})

	if got.ConfigFile != configPath {
		t.Fatalf("ConfigFile = %q, want %q", got.ConfigFile, configPath)
	}
	if !got.ConfigReadable || got.ConfigError != "" {
		t.Fatalf("config status = readable:%v error:%q", got.ConfigReadable, got.ConfigError)
	}
	if got.PanelGitHubRepository != "https://github.com/acme/panel" {
		t.Fatalf("PanelGitHubRepository = %q", got.PanelGitHubRepository)
	}
	if got.StaticDir != filepath.Join(dir, "static") {
		t.Fatalf("StaticDir = %q", got.StaticDir)
	}
}

func TestResolveHostConfigMatchesHostConfigFlagForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "long separate", args: []string{"server", "--config", "config.pro.yaml"}, want: "config.pro.yaml"},
		{name: "short separate", args: []string{"server", "-config", "config.pro.yaml"}, want: "config.pro.yaml"},
		{name: "long equals", args: []string{"server", "--config=config.pro.yaml"}, want: "config.pro.yaml"},
		{name: "short equals", args: []string{"server", "-config=config.pro.yaml"}, want: "config.pro.yaml"},
		{name: "stop parsing", args: []string{"server", "--", "--config", "ignored.yaml"}, want: filepath.Join("/work", "config.yaml")},
		{name: "default", args: []string{"server"}, want: filepath.Join("/work", "config.yaml")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveHostConfig(HostConfigEnvironment{
				Args:     tt.args,
				Getwd:    func() (string, error) { return "/work", nil },
				Getenv:   func(string) string { return "" },
				ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
				Stat:     os.Stat,
			})
			if got.ConfigFile != tt.want {
				t.Fatalf("ConfigFile = %q, want %q", got.ConfigFile, tt.want)
			}
		})
	}
}

func TestResolveHostConfigStaticDirectoryPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "management file override",
			env:  map[string]string{"MANAGEMENT_STATIC_PATH": "/srv/ui/management.html", "WRITABLE_PATH": "/ignored"},
			want: "/srv/ui",
		},
		{
			name: "management directory override",
			env:  map[string]string{"MANAGEMENT_STATIC_PATH": "/srv/ui"},
			want: "/srv/ui",
		},
		{
			name: "uppercase writable path",
			env:  map[string]string{"WRITABLE_PATH": "/data"},
			want: filepath.Join("/data", "static"),
		},
		{
			name: "lowercase writable path",
			env:  map[string]string{"writable_path": "/lower"},
			want: filepath.Join("/lower", "static"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveHostConfig(HostConfigEnvironment{
				Args:     []string{"server", "--config", "/etc/cliproxy/config.yaml"},
				Getwd:    func() (string, error) { return "/work", nil },
				Getenv:   func(key string) string { return tt.env[key] },
				ReadFile: func(string) ([]byte, error) { return []byte("{}"), nil },
				Stat:     os.Stat,
			})
			if got.StaticDir != tt.want {
				t.Fatalf("StaticDir = %q, want %q", got.StaticDir, tt.want)
			}
		})
	}
}

func TestResolveHostConfigFallsBackOnReadOrYAMLError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		readFile func(string) ([]byte, error)
	}{
		{name: "read failure", readFile: func(string) ([]byte, error) { return nil, errors.New("denied") }},
		{name: "yaml failure", readFile: func(string) ([]byte, error) { return []byte("remote-management: ["), nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveHostConfig(HostConfigEnvironment{
				Args:     []string{"server"},
				Getwd:    func() (string, error) { return "/work", nil },
				Getenv:   func(string) string { return "" },
				ReadFile: tt.readFile,
				Stat:     os.Stat,
			})
			if got.ConfigReadable {
				t.Fatal("ConfigReadable = true, want false")
			}
			if got.ConfigError == "" {
				t.Fatal("ConfigError is empty")
			}
			if got.PanelGitHubRepository != DefaultPanelGitHubRepository {
				t.Fatalf("PanelGitHubRepository = %q", got.PanelGitHubRepository)
			}
		})
	}
}
