package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"openrouter-bot/config"
)

// UserUsage is the on-disk format (logs/<id>.json), kept compatible.
type UserUsage struct {
	UserName     string    `json:"user_name"`
	UsageHistory UsageHist `json:"usage_history"`
}

type UsageHist struct {
	ChatCost map[string]float64 `json:"chat_cost"`
}

// UsageTracker tracks money spent by one Telegram user.
type UsageTracker struct {
	UserID   int64
	UserName string
	dir      string

	mu    sync.Mutex
	usage UserUsage
	now   func() time.Time
}

func newUsageTracker(userID int64, userName, dir string) *UsageTracker {
	ut := &UsageTracker{
		UserID:   userID,
		UserName: userName,
		dir:      dir,
		usage:    UserUsage{UserName: userName, UsageHistory: UsageHist{ChatCost: map[string]float64{}}},
		now:      time.Now,
	}
	if err := ut.load(); err != nil {
		log.Printf("Error loading usage for user %d: %v", userID, err)
	}
	return ut
}

func (ut *UsageTracker) file() string {
	return filepath.Join(ut.dir, fmt.Sprintf("%d.json", ut.UserID))
}

func (ut *UsageTracker) load() error {
	if ut.dir == "" {
		return nil
	}
	raw, err := os.ReadFile(ut.file())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var u UserUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return err
	}
	if u.UsageHistory.ChatCost == nil {
		u.UsageHistory.ChatCost = map[string]float64{}
	}
	if u.UserName == "" {
		u.UserName = ut.UserName
	}
	ut.mu.Lock()
	ut.usage = u
	ut.mu.Unlock()
	return nil
}

func (ut *UsageTracker) saveLocked() error {
	if ut.dir == "" {
		return nil
	}
	raw, err := json.MarshalIndent(ut.usage, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ut.dir, 0o755); err != nil {
		return err
	}
	tmp := ut.file() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ut.file())
}

// AddCost records spending for today and saves it.
func (ut *UsageTracker) AddCost(cost float64) {
	if cost <= 0 {
		return
	}
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.usage.UsageHistory.ChatCost[ut.now().Format("2006-01-02")] += cost
	if err := ut.saveLocked(); err != nil {
		log.Printf("Failed to save usage for user %d: %v", ut.UserID, err)
	}
}

// GetCurrentCost returns spending for "daily", "monthly" or "total".
func (ut *UsageTracker) GetCurrentCost(period string) float64 {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	today := ut.now().Format("2006-01-02")
	var cost float64
	for date, c := range ut.usage.UsageHistory.ChatCost {
		switch period {
		case "daily":
			if date == today {
				cost += c
			}
		case "monthly":
			if strings.HasPrefix(date, today[:7]) {
				cost += c
			}
		case "total":
			cost += c
		}
	}
	return cost
}

// HaveAccess checks the role and budget.
func (ut *UsageTracker) HaveAccess(conf *config.Config) bool {
	switch conf.RoleOf(ut.UserID) {
	case config.RoleAdmin:
		return true
	case config.RoleUser:
		// USER_BUDGET <= 0 means "no limit" for users from ALLOWED_USER_IDS.
		if conf.UserBudget <= 0 {
			return true
		}
		return ut.GetCurrentCost(conf.BudgetPeriod) < conf.UserBudget
	default:
		return conf.GuestBudget > 0 && ut.GetCurrentCost(conf.BudgetPeriod) < conf.GuestBudget
	}
}
