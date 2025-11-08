package config

import (
	"github.com/vitalvas/gokit/xconfig"
)

type Config struct {
	IMAP    IMAPConfig    `yaml:"imap"`
	Storage StorageConfig `yaml:"storage"`
}

type IMAPConfig struct {
	Host     string `yaml:"host" validate:"required"`
	Port     int    `yaml:"port" validate:"required,min=1,max=65535"`
	Username string `yaml:"username" validate:"required"`
	Password string `yaml:"password" validate:"required"`
	TLS      bool   `yaml:"tls"`
}

type StorageConfig struct {
	Path string `yaml:"path" validate:"required"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	if err := xconfig.Load(&cfg, xconfig.WithFiles(path)); err != nil {
		return nil, err
	}
	return &cfg, nil
}
