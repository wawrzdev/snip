package snip

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type providerConfig struct {
	Enabled bool
	Host    string
}

type Config struct {
	DefaultProvider string
	GitHub          providerConfig
	GitLab          providerConfig
}

func defaultConfig() Config {
	return Config{
		DefaultProvider: "github",
		GitHub:          providerConfig{Enabled: true, Host: "github.com"},
	}
}

func configPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "snip", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "snip", "config.toml"), nil
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	defer f.Close()
	section := ""
	explicitEnabled := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section != "github" && section != "gitlab" {
				return Config{}, fmt.Errorf("%s:%d: unknown section %q", path, lineNo, section)
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return Config{}, fmt.Errorf("%s:%d: expected key = value", path, lineNo)
		}
		key, raw := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if section == "" && key == "default_provider" {
			value, err := tomlString(raw)
			if err != nil {
				return Config{}, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			cfg.DefaultProvider = value
			continue
		}
		var target *providerConfig
		switch section {
		case "github":
			target = &cfg.GitHub
		case "gitlab":
			target = &cfg.GitLab
		default:
			return Config{}, fmt.Errorf("%s:%d: unknown key %q", path, lineNo, key)
		}
		switch key {
		case "enabled":
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return Config{}, fmt.Errorf("%s:%d: enabled must be true or false", path, lineNo)
			}
			target.Enabled = v
			explicitEnabled[section] = true
		case "host":
			v, err := tomlString(raw)
			if err != nil {
				return Config{}, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			target.Host = v
			if v != "" && !explicitEnabled[section] {
				target.Enabled = true
			}
		default:
			return Config{}, fmt.Errorf("%s:%d: unknown key %q in [%s]", path, lineNo, key, section)
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	if cfg.DefaultProvider != "github" && cfg.DefaultProvider != "gitlab" {
		return Config{}, fmt.Errorf("default_provider must be \"github\" or \"gitlab\"")
	}
	if cfg.GitHub.Enabled && cfg.GitHub.Host == "" {
		return Config{}, fmt.Errorf("[github].host is required when enabled")
	}
	if cfg.GitLab.Enabled && cfg.GitLab.Host == "" {
		return Config{}, fmt.Errorf("[gitlab].host is required when enabled")
	}
	return cfg, nil
}

func tomlString(raw string) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", errors.New("value must be a quoted string")
	}
	v, err := strconv.Unquote(raw)
	if err != nil {
		return "", errors.New("invalid quoted string")
	}
	return v, nil
}
