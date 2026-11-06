package config

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ReloadDelay groups the burst of file events produced by one save (editors
// often truncate the file and then write it) into a single reload.
var ReloadDelay = 500 * time.Millisecond

// Manager keeps the current config and reloads it when config.yaml changes.
type Manager struct {
	path   string
	mu     sync.RWMutex
	config *Config

	delay   time.Duration
	timerMu sync.Mutex
	timer   *time.Timer
}

func NewManager(configPath string) (*Manager, error) {
	conf, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{path: configPath, config: conf, delay: ReloadDelay}

	if configPath == "" {
		return m, nil
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		return m, nil
	}
	if v, err := newViper(configPath); err == nil {
		v.OnConfigChange(func(fsnotify.Event) { m.scheduleReload() })
		v.WatchConfig()
	}
	return m, nil
}

// NewStaticManager wraps a fixed config (used in tests).
func NewStaticManager(c *Config) *Manager { return &Manager{config: c} }

func (m *Manager) scheduleReload() {
	m.timerMu.Lock()
	defer m.timerMu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(m.delay, m.reload)
}

func (m *Manager) reload() {
	raw, err := os.ReadFile(m.path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		// Half-written or deleted file: keep the current config.
		log.Printf("Config file %s is empty or unreadable, keeping the previous config", m.path)
		return
	}
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
