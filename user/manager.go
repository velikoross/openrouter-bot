package user

import (
	"log"
	"os"
	"sync"
)

type convKey struct{ chatID, userID int64 }

// Manager owns usage trackers (per user) and conversations (per user per chat).
type Manager struct {
	dir   string
	mu    sync.Mutex
	users map[int64]*UsageTracker
	convs map[convKey]*Conversation
}

func NewUserManager(dir string) *Manager {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("Cannot create data dir %s: %v", dir, err)
		}
	}
	return &Manager{
		dir:   dir,
		users: make(map[int64]*UsageTracker),
		convs: make(map[convKey]*Conversation),
	}
}

func (m *Manager) GetUser(userID int64, userName string) *UsageTracker {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[userID]; ok {
		return u
	}
	u := newUsageTracker(userID, userName, m.dir)
	m.users[userID] = u
	return u
}

func (m *Manager) Conversation(chatID, userID int64) *Conversation {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := convKey{chatID, userID}
	if c, ok := m.convs[k]; ok {
		return c
	}
	c := newConversation()
	m.convs[k] = c
	return c
}
