package main

import (
	"fmt"
	"log"
	"openrouter-bot/api"
	"openrouter-bot/config"
	"openrouter-bot/lang"
	"openrouter-bot/user"
	"strconv"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"
)

// === Определяем роль пользователя ===
func getUserRole(userID int64, conf *config.Config) string {
	userIDStr := strconv.FormatInt(userID, 10)

	for _, adminID := range conf.AdminChatIDs {
		if userIDStr == fmt.Sprintf("%d", adminID) {
			return "admin"
		}
	}
	for _, allowedID := range conf.AllowedUserChatIDs {
		if userIDStr == fmt.Sprintf("%d", allowedID) {
			return "user"
		}
	}
	return "guest"
}

// === Динамическое меню команд ===
func getBotCommands(userID int64, conf *config.Config) []tgbotapi.BotCommand {
	role := getUserRole(userID, conf)

	if role == "admin" {
		return []tgbotapi.BotCommand{
			{Command: "start", Description: lang.Translate("description.start", conf.Lang)},
			{Command: "help", Description: lang.Translate("description.help", conf.Lang)},
			{Command: "get_models", Description: lang.Translate("description.getModels", conf.Lang)},
			{Command: "set_model", Description: lang.Translate("description.setModel", conf.Lang)},
			{Command: "reset", Description: lang.Translate("description.reset", conf.Lang)},
			{Command: "stats", Description: lang.Translate("description.stats", conf.Lang)},
			{Command: "stop", Description: lang.Translate("description.stop", conf.Lang)},
			{Command: "ask", Description: lang.Translate("description.ask", conf.Lang)},
			{Command: "pirdun", Description: lang.Translate("description.pirdun", conf.Lang)},
		}
	}

	if role == "user" {
		return []tgbotapi.BotCommand{
			{Command: "start", Description: lang.Translate("description.start", conf.Lang)},
			{Command: "help", Description: lang.Translate("description.helpuser", conf.Lang)},
			{Command: "reset", Description: lang.Translate("description.reset", conf.Lang)},
			{Command: "stop", Description: lang.Translate("description.stop", conf.Lang)},
			{Command: "ask", Description: lang.Translate("description.ask", conf.Lang)},
			{Command: "pirdun", Description: lang.Translate("description.pirdun", conf.Lang)},
		}
	}

	return []tgbotapi.BotCommand{
		{Command: "start", Description: lang.Translate("description.start", conf.Lang)},
		{Command: "ask", Description: lang.Translate("description.ask", conf.Lang)},
		{Command: "pirdun", Description: lang.Translate("description.pirdun", conf.Lang)},
	}
}

var commandsSent sync.Map

// safeGo запускает горутину с recover() — паника не уронит всего бота
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC recovered in goroutine: %v", r)
			}
		}()
		fn()
	}()
}

// handleAskCommand — общая логика для /ask и /pirdun
func handleAskCommand(
	bot *tgbotapi.BotAPI,
	client *openai.Client,
	update tgbotapi.Update,
	conf *config.Config,
	userStats *user.UsageTracker,
) {
	args := update.Message.CommandArguments()
	if args == "" {
		bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
			lang.Translate("commands.pirdun", conf.Lang)))
		return
	}
	fakeMsg := *update.Message
	fakeMsg.Text = args
	safeGo(func() {
		if userStats.HaveAccess(conf) {
			responseID := api.HandleChatGPTStreamResponse(bot, client, &fakeMsg, conf, userStats)
			if conf.Model.Type == "openrouter" && responseID != "" {
				userStats.GetUsageFromApi(responseID, conf)
			}
		} else {
			bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
				lang.Translate("budget_out", conf.Lang)))
		}
	})
}

func main() {
	if err := lang.LoadTranslations("./lang/"); err != nil {
		log.Fatalf("Error loading translations: %v", err)
	}

	manager, err := config.NewManager("./config.yaml")
	if err != nil {
		log.Fatalf("Error initializing config manager: %v", err)
	}
	conf := manager.GetConfig()

	bot, err := tgbotapi.NewBotAPI(conf.TelegramBotToken)
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false // в продакшене лучше false — меньше спама в логах

	if _, err = bot.Request(tgbotapi.DeleteWebhookConfig{}); err != nil {
		log.Fatalf("Failed to delete webhook: %v", err)
	}

	// Глобальные команды — видны в меню бота
	globalCommands := []tgbotapi.BotCommand{
		{Command: "ask", Description: lang.Translate("description.ask", conf.Lang)},
		{Command: "pirdun", Description: lang.Translate("description.pirdun", conf.Lang)},
	}
	if _, err = bot.Request(tgbotapi.NewSetMyCommands(globalCommands...)); err != nil {
		log.Printf("Failed to set global commands: %v", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	clientOptions := openai.DefaultConfig(conf.OpenAIApiKey)
	clientOptions.BaseURL = conf.OpenAIBaseURL
	client := openai.NewClientWithConfig(clientOptions)

	userManager := user.NewUserManager("logs")

	for update := range updates {
		if update.Message == nil {
			continue
		}

		// Тихо игнорируем системные события добавления в группу
		if update.Message.NewChatMembers != nil {
			continue
		}

		// Перечитываем актуальный конфиг при каждом сообщении
		// (поддерживает hot-reload config.yaml без перезапуска)
		conf = manager.GetConfig()

		userID := update.Message.From.ID
		username := ""
		if update.Message.From != nil {
			username = update.Message.From.UserName
		}
		userStats := userManager.GetUser(userID, username, conf)
		role := getUserRole(userID, conf)

		// Персональное меню команд (только для личных чатов, один раз)
		if update.Message.Chat.Type == "private" {
			if _, loaded := commandsSent.LoadOrStore(userID, struct{}{}); !loaded {
				scope := tgbotapi.NewBotCommandScopeChat(update.Message.Chat.ID)
				cfg := tgbotapi.NewSetMyCommandsWithScopeAndLanguage(
					scope,
					"",
					getBotCommands(userID, conf)...,
				)
				if _, err := bot.Request(cfg); err != nil {
					log.Printf("Failed to set per-user commands for %d: %v", userID, err)
				}
			}
		}

		if update.Message.IsCommand() {
			cmd := update.Message.Command()

			// Проверка прав для admin-команд
			if cmd == "get_models" || cmd == "set_model" || cmd == "stats" {
				if role != "admin" {
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						"Эта команда доступна только администраторам."))
					continue
				}
			}

			switch cmd {
			case "start":
				text := lang.Translate("commands.start", conf.Lang)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, text)
				msg.ParseMode = "HTML"
				bot.Send(msg)

			case "help":
				helpKey := "commands.helpuser"
				if role == "admin" {
					helpKey = "commands.help"
				}
				msg := tgbotapi.NewMessage(update.Message.Chat.ID,
					lang.Translate(helpKey, conf.Lang))
				msg.ParseMode = "HTML"
				bot.Send(msg)

			case "reset":
				args := update.Message.CommandArguments()
				userStats.ClearHistory()
				switch {
				case args == "" || args == "clear":
					userStats.SystemPrompt = conf.SystemPrompt
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						lang.Translate("commands.reset", conf.Lang)))
				case args == "system":
					userStats.SystemPrompt = conf.SystemPrompt
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						lang.Translate("commands.reset_system", conf.Lang)))
				default:
					userStats.SystemPrompt = args
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						lang.Translate("commands.reset_prompt", conf.Lang)+args))
				}

			case "stop":
				if userStats.CurrentStream != nil {
					userStats.CurrentStream.Close()
					userStats.CurrentStream = nil
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						lang.Translate("commands.stop", conf.Lang)))
				} else {
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						lang.Translate("commands.stop_err", conf.Lang)))
				}

			// /ask и /pirdun — одинаковое поведение
			case "ask", "pirdun":
				handleAskCommand(bot, client, update, conf, userStats)

			case "get_models":
				models, err := api.GetFreeModels(conf)
				if err != nil {
					log.Printf("get_models error: %v", err)
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "Ошибка получения списка моделей."))
					continue
				}
				text := lang.Translate("commands.getModels", conf.Lang) + models
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, text)
				msg.ParseMode = tgbotapi.ModeMarkdown
				bot.Send(msg)

			case "set_model":
				args := update.Message.CommandArguments()
				argsArr := strings.Split(args, " ")
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")
				msg.ParseMode = tgbotapi.ModeMarkdown
				switch {
				case args == "default":
					conf.Model.ModelName = conf.Model.ModelNameDefault
					msg.Text = lang.Translate("commands.setModel", conf.Lang) + " `" + conf.Model.ModelName + "`"
				case args == "":
					msg.Text = lang.Translate("commands.noArgsModel", conf.Lang)
				case len(argsArr) > 1:
					msg.Text = lang.Translate("commands.noSpaceModel", conf.Lang)
				default:
					conf.Model.ModelName = argsArr[0]
					msg.Text = lang.Translate("commands.setModel", conf.Lang) + " `" + conf.Model.ModelName + "`"
				}
				bot.Send(msg)

			case "stats":
				userStats.CheckHistory(conf.MaxHistorySize, conf.MaxHistoryTime)
				counted := strconv.FormatFloat(userStats.GetCurrentCost(conf.BudgetPeriod), 'f', 6, 64)
				daily := strconv.FormatFloat(userStats.GetCurrentCost("daily"), 'f', 6, 64)
				monthly := strconv.FormatFloat(userStats.GetCurrentCost("monthly"), 'f', 6, 64)
				total := strconv.FormatFloat(userStats.GetCurrentCost("total"), 'f', 6, 64)
				msgs := strconv.Itoa(len(userStats.GetMessages()))
				text := fmt.Sprintf(lang.Translate("commands.stats", conf.Lang),
					counted, daily, monthly, total, msgs)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, text)
				msg.ParseMode = "HTML"
				bot.Send(msg)
			}

		} else {
			// Обычные сообщения (не команды)
			safeGo(func() {
				if userStats.HaveAccess(conf) {
					responseID := api.HandleChatGPTStreamResponse(bot, client, update.Message, conf, userStats)
					if conf.Model.Type == "openrouter" && responseID != "" {
						userStats.GetUsageFromApi(responseID, conf)
					}
				} else {
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						lang.Translate("budget_out", conf.Lang)))
				}
			})
		}
	}
}