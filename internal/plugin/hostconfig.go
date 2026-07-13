package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPanelGitHubRepository is used when the host config is missing or has no repository setting.
const DefaultPanelGitHubRepository = "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"

const managementAssetName = "management.html"

// HostConfig captures the host configuration values the plugin needs at update time.
type HostConfig struct {
	ConfigFile            string
	ConfigReadable        bool
	ConfigError           string
	PanelGitHubRepository string
	StaticDir             string
}

// HostConfigEnvironment abstracts process state for deterministic tests.
type HostConfigEnvironment struct {
	Args     []string
	Getwd    func() (string, error)
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
	Stat     func(string) (fs.FileInfo, error)
}

// ResolveCurrentHostConfig resolves the host configuration from the live process environment.
func ResolveCurrentHostConfig() HostConfig {
	return ResolveHostConfig(HostConfigEnvironment{
		Args:     os.Args,
		Getwd:    os.Getwd,
		Getenv:   os.Getenv,
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
	})
}

// ResolveHostConfig mirrors the host's --config parsing and reads the panel repository scalar.
func ResolveHostConfig(env HostConfigEnvironment) HostConfig {
	configFile := resolveConfigFile(env.Args, env.Getwd)
	out := HostConfig{
		ConfigFile:            configFile,
		PanelGitHubRepository: DefaultPanelGitHubRepository,
		StaticDir:             resolveStaticDir(configFile, env.Getenv, env.Stat),
	}

	raw, errRead := env.ReadFile(configFile)
	if errRead != nil {
		out.ConfigError = fmt.Sprintf("read host config: %v", errRead)
		return out
	}

	var parsed struct {
		RemoteManagement struct {
			PanelGitHubRepository string `yaml:"panel-github-repository"`
		} `yaml:"remote-management"`
	}
	if errUnmarshal := yaml.Unmarshal(raw, &parsed); errUnmarshal != nil {
		out.ConfigError = fmt.Sprintf("parse host config: %v", errUnmarshal)
		return out
	}

	out.ConfigReadable = true
	if repository := strings.TrimSpace(parsed.RemoteManagement.PanelGitHubRepository); repository != "" {
		out.PanelGitHubRepository = repository
	}
	return out
}

func resolveConfigFile(args []string, getwd func() (string, error)) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--":
			return defaultConfigFile(getwd)
		case arg == "-config" || arg == "--config":
			if index+1 < len(args) {
				return args[index+1]
			}
			return defaultConfigFile(getwd)
		case strings.HasPrefix(arg, "-config="):
			return strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			return strings.TrimPrefix(arg, "--config=")
		}
	}
	return defaultConfigFile(getwd)
}

func defaultConfigFile(getwd func() (string, error)) string {
	wd, errGetwd := getwd()
	if errGetwd != nil {
		return "config.yaml"
	}
	return filepath.Join(wd, "config.yaml")
}

func resolveStaticDir(configFile string, getenv func(string) string, stat func(string) (fs.FileInfo, error)) string {
	if override := strings.TrimSpace(getenv("MANAGEMENT_STATIC_PATH")); override != "" {
		cleaned := filepath.Clean(override)
		if strings.EqualFold(filepath.Base(cleaned), managementAssetName) {
			return filepath.Dir(cleaned)
		}
		return cleaned
	}

	for _, key := range []string{"WRITABLE_PATH", "writable_path"} {
		if writable := strings.TrimSpace(getenv(key)); writable != "" {
			return filepath.Join(filepath.Clean(writable), "static")
		}
	}

	base := filepath.Dir(configFile)
	if info, errStat := stat(configFile); errStat == nil && info.IsDir() {
		base = configFile
	}
	return filepath.Join(base, "static")
}
