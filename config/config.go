// Package config loads bot settings from config.yaml, the .env file and the
// process environment (environment variables win over config.yaml).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"

	"openrouter-bot/lang"
)

// Role of a Telegram user.
type Role string

const (
	RoleAdmin Role = "ADMIN"
	RoleUser  Role = "USER"
	RoleGuest Role = "GUEST"
)

type Config struct {
	TelegramBotToken   string
	OpenAIApiKey       string
	Model              ModelParameters
	MaxTokens          int
	OpenAIBaseURL      string
	SystemPrompt       string
	BudgetPeriod       string
	GuestBudget        float64
	UserBudget         float64
	AdminChatIDs       []int64
	AllowedUserChatIDs []int64
	MaxHistorySize     int
	MaxHistoryTime     int
	Vision             bool
	VisionPrompt       string
	VisionDetails      string
	StatsMinRole       string
	Lang               string
	// BotAliases are short names ("bot", "пирдун") the bot reacts to in group
	// chats in addition to @username mentions and replies.
	BotAliases []string
	// ModelPresets are shown as buttons by /set_model without arguments.
	ModelPresets []string
	// DataDir stores usage statistics and runtime state (selected model).
	DataDir        string
	RequestTimeout time.Duration
}

type ModelParameters struct {
	Type             string
	ModelName        string
	FrequencyPenalty float64
	PresencePenalty  float64
	Temperature      float64
	TopP             float64
}

// RoleOf returns the role of a Telegram user id.
func (c *Config) RoleOf(userID int64) Role {
	for _, id := range c.AdminChatIDs {
		if id == userID {
			return RoleAdmin
		}
	}
	for _, id := range c.AllowedUserChatIDs {
		if id == userID {
			return RoleUser
		}
	}
	return RoleGuest
}

// IsAdmin reports whether the user is listed in ADMIN_IDS.
func (c *Config) IsAdmin(userID int64) bool { return c.RoleOf(userID) == RoleAdmin }

// CanViewStats applies STATS_MIN_ROLE.
func (c *Config) CanViewStats(userID int64) bool {
	switch c.RoleOf(userID) {
	case RoleAdmin:
		return true
	case RoleUser:
		return c.StatsMinRole == string(RoleUser) || c.StatsMinRole == string(RoleGuest)
	default:
		return c.StatsMinRole == string(RoleGuest)
	}
}

func newViper(configPath string) (*viper.Viper, error) {
	v := viper.New()
	v.SetDefault("MAX_TOKENS", 8000) // reasoning models spend part of it on thinking
	v.SetDefault("TEMPERATURE", 0.7)
	v.SetDefault("TOP_P", 0.7)
	v.SetDefault("TYPE", "openrouter")
	v.SetDefault("BASE_URL", "https://openrouter.ai/api/v1")
	v.SetDefault("BUDGET_PERIOD", "monthly")
	v.SetDefault("MAX_HISTORY_SIZE", 10)
	v.SetDefault("MAX_HISTORY_TIME", 60)
	v.SetDefault("VISION_PROMPT", "Describe the image")
	v.SetDefault("VISION_DETAIL", "low")
	v.SetDefault("STATS_MIN_ROLE", "ADMIN")
	v.SetDefault("DATA_DIR", "logs")
	v.SetDefault("REQUEST_TIMEOUT", 300)

	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("read %s: %w", configPath, err)
			}
			log.Printf("Config file %s not found, using environment only", configPath)
		}
	}
	return v, nil
}

// Load reads configuration. A missing .env or config.yaml is not an error:
// on a hosting platform everything can come from real environment variables.
func Load(configPath string) (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("Failed to parse .env: %v", err)
	}

	v, err := newViper(configPath)
	if err != nil {
		return nil, err
	}
	// Values from the file only, read before env overrides are enabled. Used
	// for "lang", because the LANG env variable is the system locale.
	fileLang := v.GetString("lang")
	v.AutomaticEnv()

	conf := &Config{
		TelegramBotToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		OpenAIApiKey:     strings.TrimSpace(os.Getenv("API_KEY")),
		Model: ModelParameters{
			Type:             strings.ToLower(v.GetString("TYPE")),
			ModelName:        strings.TrimSpace(v.GetString("MODEL")),
			FrequencyPenalty: v.GetFloat64("FREQUENCY_PENALTY"),
			PresencePenalty:  v.GetFloat64("PRESENCE_PENALTY"),
			Temperature:      v.GetFloat64("TEMPERATURE"),
			TopP:             v.GetFloat64("TOP_P"),
		},
		MaxTokens:          v.GetInt("MAX_TOKENS"),
		OpenAIBaseURL:      strings.TrimRight(strings.TrimSpace(v.GetString("BASE_URL")), "/"),
		BudgetPeriod:       strings.ToLower(v.GetString("BUDGET_PERIOD")),
		GuestBudget:        v.GetFloat64("GUEST_BUDGET"),
		UserBudget:         v.GetFloat64("USER_BUDGET"),
		AdminChatIDs:       parseIDList("ADMIN_IDS", v.GetString("ADMIN_IDS")),
		AllowedUserChatIDs: parseIDList("ALLOWED_USER_IDS", v.GetString("ALLOWED_USER_IDS")),
		MaxHistorySize:     v.GetInt("MAX_HISTORY_SIZE"),
		MaxHistoryTime:     v.GetInt("MAX_HISTORY_TIME"),
		Vision:             v.GetBool("VISION"),
		VisionPrompt:       v.GetString("VISION_PROMPT"),
		VisionDetails:      v.GetString("VISION_DETAIL"),
		StatsMinRole:       strings.ToUpper(v.GetString("STATS_MIN_ROLE")),
		Lang:               resolveLang(os.Getenv("BOT_LANG"), os.Getenv("LANG"), fileLang),
		BotAliases:         parseList(v.Get("BOT_ALIASES")),
		ModelPresets:       parseList(v.Get("MODEL_PRESETS")),
		DataDir:            v.GetString("DATA_DIR"),
		RequestTimeout:     time.Duration(v.GetInt("REQUEST_TIMEOUT")) * time.Second,
	}

	switch conf.BudgetPeriod {
	case "daily", "monthly", "total":
	default:
		return nil, fmt.Errorf("invalid budget_period %q (use daily, monthly or total)", conf.BudgetPeriod)
	}
	if conf.MaxHistorySize < 0 {
		conf.MaxHistorySize = 0
	}
	if conf.RequestTimeout <= 0 {
		conf.RequestTimeout = 5 * time.Minute
	}

	language := lang.Translate("language", conf.Lang)
	prompt := strings.TrimSpace(v.GetString("ASSISTANT_PROMPT"))
	conf.SystemPrompt = strings.TrimSpace("Always answer in " + language + " language. " + prompt)
	return conf, nil
}

// Validate reports problems that make the bot unusable.
func (c *Config) Validate() error {
	var errs []string
	if c.TelegramBotToken == "" {
		errs = append(errs, "TELEGRAM_BOT_TOKEN is empty")
	}
	if c.Model.ModelName == "" {
		errs = append(errs, "MODEL is empty")
	}
	if c.OpenAIBaseURL == "" {
		errs = append(errs, "BASE_URL is empty")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// resolveLang picks the bot language: BOT_LANG env, then LANG env (only if it
// is literally EN/RU, not a system locale like en_US.UTF-8), then config file.
func resolveLang(botLang, envLang, fileLang string) string {
	for _, candidate := range []string{botLang, envLang, fileLang} {
		if l := lang.Normalize(candidate); l != "" {
			return l
		}
	}
	return lang.Default
}

func parseIDList(name, value string) []int64 {
	var ids []int64
	for _, s := range parseList(value) {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			log.Printf("Invalid id %q in %s: %v", s, name, err)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// parseList accepts a comma separated string or a YAML list.
func parseList(raw interface{}) []string {
	var parts []string
	switch val := raw.(type) {
	case nil:
		return nil
	case string:
		parts = strings.Split(val, ",")
	case []interface{}:
		for _, p := range val {
			parts = append(parts, fmt.Sprint(p))
		}
	case []string:
		parts = val
	default:
		parts = strings.Split(fmt.Sprint(val), ",")
	}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// String renders the config for logs with secrets masked.
func (c *Config) String() string {
	fields := map[string]interface{}{
		"TelegramBotToken": mask(c.TelegramBotToken),
		"OpenAIApiKey":     mask(c.OpenAIApiKey),
		"Model":            c.Model.ModelName,
		"Type":             c.Model.Type,
		"BaseURL":          c.OpenAIBaseURL,
		"MaxTokens":        c.MaxTokens,
		"Temperature":      c.Model.Temperature,
		"TopP":             c.Model.TopP,
		"BudgetPeriod":     c.BudgetPeriod,
		"GuestBudget":      c.GuestBudget,
		"UserBudget":       c.UserBudget,
		"AdminIDs":         c.AdminChatIDs,
		"AllowedUserIDs":   c.AllowedUserChatIDs,
		"MaxHistorySize":   c.MaxHistorySize,
		"MaxHistoryTime":   c.MaxHistoryTime,
		"Vision":           c.Vision,
		"StatsMinRole":     c.StatsMinRole,
		"Lang":             c.Lang,
		"BotAliases":       c.BotAliases,
		"ModelPresets":     c.ModelPresets,
		"DataDir":          c.DataDir,
		"RequestTimeout":   c.RequestTimeout,
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %v\n", k, fields[k])
	}
	return b.String()
}

func mask(s string) string {
	if s == "" {
		return "<empty>"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
