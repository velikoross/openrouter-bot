package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"

	"openrouter-bot/api"
	"openrouter-bot/bot"
	"openrouter-bot/config"
	"openrouter-bot/lang"
	"openrouter-bot/state"
	"openrouter-bot/user"
)

func main() {
	if err := lang.LoadTranslations(); err != nil {
		log.Fatalf("Error loading translations: %v", err)
	}

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}
	manager, err := config.NewManager(configPath)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}
	conf := manager.GetConfig()
	log.Printf("Configuration:\n%s", conf)
	if err := conf.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	if len(conf.AdminChatIDs) == 0 {
		log.Printf("WARNING: ADMIN_IDS is empty, nobody can change the model from Telegram")
	}

	endpoint := os.Getenv("TELEGRAM_API_ENDPOINT") // e.g. a local Bot API server
	if endpoint == "" {
		endpoint = tgbotapi.APIEndpoint
	}
	tg, err := tgbotapi.NewBotAPIWithClient(conf.TelegramBotToken, endpoint, &http.Client{Timeout: 90 * time.Second})
	if err != nil {
		log.Fatalf("Telegram login failed (check TELEGRAM_BOT_TOKEN): %v", err)
	}
	log.Printf("Authorized as @%s (id %d)", tg.Self.UserName, tg.Self.ID)
	if !tg.Self.CanReadAllGroupMessages {
		log.Printf("NOTE: privacy mode is ON. In groups the bot only sees commands, replies and @mentions; "+
			"short names (BOT_ALIASES) and plain /ask without @%s may not reach it. "+
			"Disable it in @BotFather: /setprivacy -> Disable, then re-add the bot to the group.", tg.Self.UserName)
	}
	if _, err := tg.Request(tgbotapi.DeleteWebhookConfig{}); err != nil {
		log.Printf("Failed to delete webhook: %v", err)
	}

	store, err := state.Open(filepath.Join(conf.DataDir, "bot_state.json"))
	if err != nil {
		log.Fatalf("Error loading state: %v", err)
	}
	if m := store.Model(); m != "" {
		log.Printf("Using model selected from Telegram: %s", m)
	}

	llmConf := openai.DefaultConfig(conf.OpenAIApiKey)
	llmConf.BaseURL = conf.OpenAIBaseURL
	llmConf.HTTPClient = &http.Client{Timeout: conf.RequestTimeout + 10*time.Second}

	b := bot.New(bot.Options{
		Telegram: tg,
		Self:     tg.Self,
		LLM:      openai.NewClientWithConfig(llmConf),
		Catalog:  api.NewClient(conf.OpenAIBaseURL, conf.OpenAIApiKey),
		Config:   manager.GetConfig,
		State:    store,
		Users:    user.NewUserManager(conf.DataDir),
	})
	b.RegisterCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "callback_query"}
	updates := tg.GetUpdatesChan(u)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Bot started")

	for {
		select {
		case <-ctx.Done():
			log.Printf("Shutting down...")
			tg.StopReceivingUpdates()
			done := make(chan struct{})
			go func() { b.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			b.HandleUpdate(update)
		}
	}
}
