package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	DataDir        string
	Port           int
	Host           string
	Version        string
	ReposDir       string
	BuildsDir      string
	CaddyDir       string
	DBPath         string
	DockerNetwork  string
	CaddyContainer string
}

func New(dataDir string, port int) (*Config, error) {
	cfg := &Config{
		DataDir:        dataDir,
		Port:           port,
		ReposDir:       filepath.Join(dataDir, "repos"),
		BuildsDir:      filepath.Join(dataDir, "builds"),
		CaddyDir:       filepath.Join(dataDir, "caddy"),
		DBPath:         filepath.Join(dataDir, "stackfly.db"),
		DockerNetwork:  "stackfly-net",
		CaddyContainer: "stackfly-caddy",
	}

	dirs := []string{cfg.DataDir, cfg.ReposDir, cfg.BuildsDir, cfg.CaddyDir}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}
