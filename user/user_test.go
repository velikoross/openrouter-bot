package user

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"openrouter-bot/config"
)

func TestConversationHistory(t *testing.T) {
	c := newConversation()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	for i := 0; i < 4; i++ {
		c.AddExchange("q", "a", 6)
	}
	if c.Len() != 6 {
		t.Errorf("len = %d, want trimmed to 6", c.Len())
	}
	msgs := c.Messages(4, 60)
	if len(msgs) != 4 || msgs[0].Role != "user" {
		t.Errorf("messages = %+v", msgs)
	}
	msgs[0].Content = "mutated"
	if c.Messages(10, 60)[0].Content == "mutated" {
		t.Error("Messages must return a copy")
	}

	now = now.Add(61 * time.Minute)
	if got := c.Messages(10, 60); len(got) != 0 {
		t.Errorf("history should expire, got %d", len(got))
	}
}

func TestConversationSystemPrompt(t *testing.T) {
	c := newConversation()
	if c.SystemPrompt("def") != "def" {
		t.Error("default")
	}
	c.SetSystemPrompt("custom")
	if c.SystemPrompt("def") != "custom" {
		t.Error("custom")
	}
}

func TestConversationStop(t *testing.T) {
	c := newConversation()
	if c.Stop() {
		t.Error("nothing to stop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := c.StartRequest(cancel)
	if !c.Stop() || ctx.Err() == nil {
		t.Error("stop should cancel the request")
	}
	done()
	if c.Stop() {
		t.Error("finished request can't be stopped")
	}

	// A finished old request must not clear the cancel of a newer one.
	_, cancel1 := context.WithCancel(context.Background())
	done1 := c.StartRequest(cancel1)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := c.StartRequest(cancel2)
	done1()
	if !c.Stop() || ctx2.Err() == nil {
		t.Error("newer request should still be stoppable")
	}
	done2()
}

func TestUsageCostsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	ut := newUsageTracker(42, "bob", dir)
	day := time.Date(2026, 3, 15, 10, 0, 0, 0, time.Local)
	ut.now = func() time.Time { return day }
	ut.AddCost(0.5)
	ut.AddCost(-1) // ignored
	day = day.AddDate(0, 0, -1)
	ut.AddCost(0.25)
	day = day.AddDate(0, -1, 0)
	ut.AddCost(1)
	day = time.Date(2026, 3, 15, 10, 0, 0, 0, time.Local)

	if got := ut.GetCurrentCost("daily"); got != 0.5 {
		t.Errorf("daily = %v", got)
	}
	if got := ut.GetCurrentCost("monthly"); got != 0.75 {
		t.Errorf("monthly = %v", got)
	}
	if got := ut.GetCurrentCost("total"); got != 1.75 {
		t.Errorf("total = %v", got)
	}
	if got := ut.GetCurrentCost("weekly"); got != 0 {
		t.Errorf("unknown period = %v", got)
	}

	if _, err := os.Stat(filepath.Join(dir, "42.json")); err != nil {
		t.Fatal(err)
	}
	again := newUsageTracker(42, "bob", dir)
	if got := again.GetCurrentCost("total"); got != 1.75 {
		t.Errorf("reloaded total = %v", got)
	}
}

func TestLoadsLegacyUsageFile(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"user_name": "old", "usage_history": {"chat_cost": {"2020-01-01": 0.1}}}`
	_ = os.WriteFile(filepath.Join(dir, "7.json"), []byte(legacy), 0o644)
	ut := newUsageTracker(7, "new", dir)
	if ut.GetCurrentCost("total") != 0.1 {
		t.Error("legacy file not loaded")
	}
}

func TestHaveAccess(t *testing.T) {
	conf := &config.Config{AdminChatIDs: []int64{1}, AllowedUserChatIDs: []int64{2},
		UserBudget: 1, GuestBudget: 0.5, BudgetPeriod: "total"}
	m := NewUserManager(t.TempDir())

	admin, usr, guest := m.GetUser(1, ""), m.GetUser(2, ""), m.GetUser(3, "")
	admin.AddCost(100)
	if !admin.HaveAccess(conf) {
		t.Error("admin is unlimited")
	}
	if !usr.HaveAccess(conf) || !guest.HaveAccess(conf) {
		t.Error("within budget")
	}
	usr.AddCost(1)
	guest.AddCost(0.5)
	if usr.HaveAccess(conf) || guest.HaveAccess(conf) {
		t.Error("budget exhausted")
	}
	conf.UserBudget = 0
	if !usr.HaveAccess(conf) {
		t.Error("USER_BUDGET=0 means unlimited for allowed users")
	}
	conf.GuestBudget = 0
	if m.GetUser(4, "").HaveAccess(conf) {
		t.Error("GUEST_BUDGET=0 disables guests")
	}
}

func TestManagerReturnsSameObjects(t *testing.T) {
	m := NewUserManager("")
	if m.GetUser(1, "") != m.GetUser(1, "") {
		t.Error("users")
	}
	if m.Conversation(10, 1) != m.Conversation(10, 1) || m.Conversation(10, 1) == m.Conversation(11, 1) {
		t.Error("conversations are per chat")
	}
}
