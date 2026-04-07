package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port        int        `yaml:"port"`
	Seed        uint64     `yaml:"seed"`
	BlockTimeMs int        `yaml:"blockTimeMs"`
	Bankroll    BankrollCfg `yaml:"bankroll"`
	Games       []GameCfg  `yaml:"games"`
	Faucet      FaucetCfg  `yaml:"faucet"`
}

type BankrollCfg struct {
	Deposit uint64 `yaml:"deposit"`
	Name    string `yaml:"name"`
}

type GameCfg struct {
	Name        string `yaml:"name"`
	Wasm        string `yaml:"wasm"`
	CalcID      uint64 `yaml:"calcId"`
	HouseEdgeBp uint64 `yaml:"houseEdgeBp"`
}

type FaucetCfg struct {
	Amount uint64 `yaml:"amount"`
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
	if cfg.Port == 0 {
		cfg.Port = 4000
	}
	if cfg.Seed == 0 {
		cfg.Seed = 42
	}
	if cfg.BlockTimeMs == 0 {
		cfg.BlockTimeMs = 500
	}
	if cfg.Faucet.Amount == 0 {
		cfg.Faucet.Amount = 100_000_000
	}
	return &cfg, nil
}
