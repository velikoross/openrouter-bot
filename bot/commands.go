package bot

import (
	"fmt"
	"log"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"openrouter-bot/config"
	"openrouter-bot/lang"
)

var adminCommands = map[string]bool{
	"model": true, "set_model": true, "get_models": true, "models": true,
}

func (b *Bot) handleCommand(msg *tgbotapi.Message, cmd string) {
	conf := b.conf()
	if adminCommands[cmd] && !conf.IsAdmin(msg.From.ID) {
		b.reply(msg, b.t("admin_only"))
		return
	}
	args := msg.CommandArguments()

	switch cmd {
	case "start":
		text := b.t("commands.start") + b.t("commands.start_end")
		if !msg.Chat.IsPrivate() {
			text += fmt.Sprintf(b.t("commands.start_group"), escapeHTML(b.self.UserName))
		}
		b.reply(msg, text)

	case "help":
		text := b.t("commands.help")
		if conf.IsAdmin(msg.From.ID) {
			text += b.t("commands.help_admin")
		}
		b.reply(msg, text)

	case "reset":
		conv := b.users.Conversation(msg.Chat.ID, msg.From.ID)
		conv.Clear()
		switch args {
		case "", "clear":
			conv.SetSystemPrompt("")
			b.reply(msg, b.t("commands.reset"))
		case "system":
			conv.SetSystemPrompt("")
			b.reply(msg, b.t("commands.reset_system"))
		default:
			conv.SetSystemPrompt(args)
			b.reply(msg, b.t("commands.reset_prompt")+escapeHTML(args))
		}

	case "stop":
		if b.users.Conversation(msg.Chat.ID, msg.From.ID).Stop() {
			b.reply(msg, b.t("commands.stop"))
		} else {
			b.reply(msg, b.t("commands.stop_err"))
		}

	case "ask", "pirdun":
		if args == "" && b.quotedText(msg) == "" && len(pickPhoto(msg)) == 0 {
			b.reply(msg, b.t("commands.pirdun"))
			return
		}
		b.goSafe(func() { b.ask(msg, args) })

	case "stats":
		if !conf.CanViewStats(msg.From.ID) {
			b.reply(msg, b.t("admin_only"))
			return
		}
		u := b.users.GetUser(msg.From.ID, msg.From.UserName)
		f := func(p string) string { return strconv.FormatFloat(u.GetCurrentCost(p), 'f', 6, 64) }
		n := b.users.Conversation(msg.Chat.ID, msg.From.ID).Len()
		b.replyf(msg, "commands.stats", f(conf.BudgetPeriod), f("daily"), f("monthly"), f("total"), strconv.Itoa(n))

	case "model":
		b.replyf(msg, "commands.currentModel",
			escapeHTML(b.state.EffectiveModel(conf.Model.ModelName)), escapeHTML(conf.Model.ModelName))

	case "get_models", "models":
		b.goSafe(func() { b.listModels(msg, args) })

	case "set_model":
		b.goSafe(func() { b.setModelCommand(msg, args) })
	}
}

// commandList is shown in the Telegram command menu.
func commandList(conf *config.Config, admin bool) []tgbotapi.BotCommand {
	d := func(k string) string { return lang.Translate("description."+k, conf.Lang) }
	cmds := []tgbotapi.BotCommand{
		{Command: "ask", Description: d("ask")},
		{Command: "pirdun", Description: d("pirdun")},
		{Command: "reset", Description: d("reset")},
		{Command: "stop", Description: d("stop")},
		{Command: "help", Description: d("help")},
		{Command: "start", Description: d("start")},
	}
	if admin || conf.StatsMinRole != string(config.RoleAdmin) {
		cmds = append(cmds, tgbotapi.BotCommand{Command: "stats", Description: d("stats")})
	}
	if admin {
		cmds = append(cmds,
			tgbotapi.BotCommand{Command: "model", Description: d("model")},
			tgbotapi.BotCommand{Command: "set_model", Description: d("setModel")},
			tgbotapi.BotCommand{Command: "get_models", Description: d("getModels")},
		)
	}
	return cmds
}

// RegisterCommands sets the command menu: a common one for everybody and an
// extended one in the private chats of admins.
func (b *Bot) RegisterCommands() {
	conf := b.conf()
	if _, err := b.tg.Request(tgbotapi.NewSetMyCommands(commandList(conf, false)...)); err != nil {
		log.Printf("Failed to set commands: %v", err)
	}
	for _, id := range conf.AdminChatIDs {
		cfg := tgbotapi.NewSetMyCommandsWithScope(tgbotapi.NewBotCommandScopeChat(id), commandList(conf, true)...)
		if _, err := b.tg.Request(cfg); err != nil {
			// Fails if the admin has never started a chat with the bot.
			log.Printf("Failed to set admin commands for %d: %v", id, err)
		}
	}
}
