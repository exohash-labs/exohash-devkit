package bots

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	BffURL string        `yaml:"bffUrl"`
	Crash  []CrashCfg    `yaml:"crash"`
	Dice   []DiceCfg     `yaml:"dice"`
	Mines  []MinesCfg    `yaml:"mines"`
}

type CrashCfg struct {
	Name    string `yaml:"name"`
	Stake   uint64 `yaml:"stake"`
	Cashout uint64 `yaml:"cashout"` // bp target
}

type DiceCfg struct {
	Name     string `yaml:"name"`
	Stake    uint64 `yaml:"stake"`
	ChanceBP uint64 `yaml:"chanceBp"`
	Every    int    `yaml:"every"`
}

type MinesCfg struct {
	Name    string `yaml:"name"`
	Stake   uint64 `yaml:"stake"`
	Mines   int    `yaml:"mines"`
	Reveals int    `yaml:"reveals"`
	Every   int    `yaml:"every"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.BffURL == "" {
		cfg.BffURL = "http://localhost:3100" // bffsim and real chain's BFF both on :3100
	}
	return &cfg, nil
}
