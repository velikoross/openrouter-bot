package config

import (
	"log"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Manager keeps the current config and reloads it when config.yaml changes.
type Manager struct {
	path   string
	mu     sync.RWMutex
	config *Config
}

func NewManager(configPath string) (*Manager, error) {
	conf, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{path: configPath, config: conf}

	if configPath == "" {
		return m, nil
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		return m, nil
	}
	if v, err := newViper(configPath); err == nil {
		v.OnConfigChange(func(fsnotify.Event) { m.reload() })
		v.WatchConfig()
	}
	return m, nil
}

// NewStaticManager wraps a fixed config (used in tests).
func NewStaticManager(c *Config) *Manager { return &Manager{config: c} }

func (m *Manager) reload() {
	conf, err := Load(m.path)
	if err != nil {
		log.Printf("Failed to reload config, keeping the previous one: %v", err)
		return
	}
	m.mu.Lock()
	m.config = conf
	m.mu.Unlock()
	log.Printf("Config reloaded")
}

// GetConfig returns the current config. Callers must treat it as read-only.
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}
