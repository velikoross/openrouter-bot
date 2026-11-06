package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var envKeys = []string{
	"TELEGRAM_BOT_TOKEN", "API_KEY", "MODEL", "TYPE", "BASE_URL", "ADMIN_IDS", "ALLOWED_USER_IDS",
	"LANG", "BOT_LANG", "VISION", "BOT_ALIASES", "MODEL_PRESETS", "BUDGET_PERIOD", "GUEST_BUDGET",
	"USER_BUDGET", "MAX_HISTORY_SIZE", "MAX_HISTORY_TIME", "ASSISTANT_PROMPT", "DATA_DIR",
	"REQUEST_TIMEOUT", "STATS_MIN_ROLE",
}

func clearEnv(t *testing.T) {
	for _, k := range envKeys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func writeYAML(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFromYAMLAndEnv(t *testing.T) {
	clearEnv(t)
	p := writeYAML(t, `
admin_ids: "111, 222"
allowed_user_ids: 333
model: from/yaml
lang: RU
vision: true
bot_aliases: ["Пирдун", "бот"]
model_presets:
  - a/b:free
  - c/d
assistant_prompt: |
  You are helpful.
`)
	t.Setenv("TELEGRAM_BOT_TOKEN", " token ")
	t.Setenv("MODEL", "from/env")
	t.Setenv("BASE_URL", "https://example.com/v1/")

	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.TelegramBotToken != "token" {
		t.Errorf("token = %q", c.TelegramBotToken)
	}
	if c.Model.ModelName != "from/env" {
		t.Errorf("env must override yaml, model = %q", c.Model.ModelName)
	}
	if c.OpenAIBaseURL != "https://example.com/v1" {
		t.Errorf("base url = %q", c.OpenAIBaseURL)
	}
	if len(c.AdminChatIDs) != 2 || c.AdminChatIDs[1] != 222 || len(c.AllowedUserChatIDs) != 1 || c.AllowedUserChatIDs[0] != 333 {
		t.Errorf("ids: %v %v", c.AdminChatIDs, c.AllowedUserChatIDs)
	}
	if !c.Vision {
		t.Error("vision should be true")
	}
	if strings.Join(c.BotAliases, ",") != "Пирдун,бот" || strings.Join(c.ModelPresets, ",") != "a/b:free,c/d" {
		t.Errorf("lists: %v %v", c.BotAliases, c.ModelPresets)
	}
	if c.Lang != "RU" || c.SystemPrompt != "Always answer in russian language. You are helpful." {
		t.Errorf("lang %q prompt %q", c.Lang, c.SystemPrompt)
	}
	// defaults
	if c.MaxTokens != 2000 || c.BudgetPeriod != "monthly" || c.DataDir != "logs" || c.RequestTimeout != 300*time.Second || c.Model.Type != "openrouter" {
		t.Errorf("defaults: %+v", c)
	}
}

func TestEnvListsAreCommaSeparated(t *testing.T) {
	clearEnv(t)
	t.Setenv("BOT_ALIASES", "бот, пирдун ,")
	t.Setenv("ADMIN_IDS", "1,abc,2")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.BotAliases, "|") != "бот|пирдун" {
		t.Errorf("aliases = %q", c.BotAliases)
	}
	if len(c.AdminChatIDs) != 2 {
		t.Errorf("invalid ids should be skipped: %v", c.AdminChatIDs)
	}
}

func TestSystemLocaleDoesNotBreakLanguage(t *testing.T) {
	clearEnv(t)
	p := writeYAML(t, "lang: RU\n")
	t.Setenv("LANG", "en_US.UTF-8") // set by most Linux hosts
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Lang != "RU" {
		t.Errorf("lang = %q, system locale must be ignored", c.Lang)
	}

	t.Setenv("LANG", "en") // legacy: LANG=EN in .env still works
	if c, _ = Load(p); c.Lang != "EN" {
		t.Errorf("lang = %q", c.Lang)
	}
	t.Setenv("BOT_LANG", "ru")
	if c, _ = Load(p); c.Lang != "RU" {
		t.Errorf("BOT_LANG should win, lang = %q", c.Lang)
	}
}

func TestMissingFilesAreFine(t *testing.T) {
	clearEnv(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "x")
	t.Setenv("MODEL", "m")
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("missing config.yaml must not be fatal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Error(err)
	}
	if c.Lang != "EN" {
		t.Errorf("lang = %q", c.Lang)
	}
}

func TestInvalidBudgetPeriod(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUDGET_PERIOD", "weekly")
	if _, err := Load(""); err == nil {
		t.Error("expected an error")
	}
}

func TestValidate(t *testing.T) {
	err := (&Config{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") || !strings.Contains(err.Error(), "MODEL") {
		t.Errorf("err = %v", err)
	}
}

func TestRoles(t *testing.T) {
	c := &Config{AdminChatIDs: []int64{1}, AllowedUserChatIDs: []int64{2}, StatsMinRole: "ADMIN"}
	if c.RoleOf(1) != RoleAdmin || c.RoleOf(2) != RoleUser || c.RoleOf(3) != RoleGuest {
		t.Error("roles")
	}
	if !c.IsAdmin(1) || c.IsAdmin(2) {
		t.Error("IsAdmin")
	}
	if !c.CanViewStats(1) || c.CanViewStats(2) || c.CanViewStats(3) {
		t.Error("stats ADMIN")
	}
	c.StatsMinRole = "USER"
	if !c.CanViewStats(2) || c.CanViewStats(3) {
		t.Error("stats USER")
	}
	c.StatsMinRole = "GUEST"
	if !c.CanViewStats(3) {
		t.Error("stats GUEST")
	}
}

func TestStringMasksSecrets(t *testing.T) {
	c := &Config{TelegramBotToken: "123456789:ABCDEFGHIJKLMNOP", OpenAIApiKey: "sk-or-v1-0123456789abcdef"}
	s := c.String()
	if strings.Contains(s, "ABCDEFGHIJKLMNOP") || strings.Contains(s, "0123456789abcdef") {
		t.Errorf("secrets leaked: %s", s)
	}
}

func TestManagerReloadsConfig(t *testing.T) {
	clearEnv(t)
	ReloadDelay = 50 * time.Millisecond
	p := writeYAML(t, "model: first\n")
	m, err := NewManager(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.GetConfig().Model.ModelName != "first" {
		t.Fatal(m.GetConfig().Model.ModelName)
	}
	if err := os.WriteFile(p, []byte("model: second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for m.GetConfig().Model.ModelName != "second" {
		if time.Now().After(deadline) {
			t.Fatalf("config was not reloaded")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A broken or empty file keeps the previous config.
	for _, body := range []string{"budget_period: weekly\n", ""} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if m.GetConfig().Model.ModelName != "second" {
			t.Errorf("config %q must not replace a valid one", body)
		}
	}
}

// One save in an editor = truncate + write: the empty intermediate state must
// never become the active config.
func TestManagerIgnoresTruncateDuringSave(t *testing.T) {
	clearEnv(t)
	ReloadDelay = 50 * time.Millisecond
	p := writeYAML(t, "model: first\n")
	m, err := NewManager(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		f, err := os.OpenFile(p, os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		_, _ = f.WriteString("model: first\n")
		f.Close()
		if got := m.GetConfig().Model.ModelName; got != "first" {
			t.Fatalf("iteration %d: model = %q", i, got)
		}
	}
	// The final reload happens after the burst settles.
	time.Sleep(200 * time.Millisecond)
	if got := m.GetConfig().Model.ModelName; got != "first" {
		t.Fatalf("model = %q", got)
	}
}
