package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"openrouter-bot/api"
)

const (
	callbackModel   = "m:"
	callbackDefault = "m:-"
	maxSearchResult = 60
	maxSuggestions  = 10
)

func (b *Bot) fetchModels() ([]api.Model, error) {
	if b.catalog == nil {
		return nil, fmt.Errorf("model catalog is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return b.catalog.ListModels(ctx)
}

// listModels: /get_models → free models; /get_models <text> → search all.
func (b *Bot) listModels(msg *tgbotapi.Message, query string) {
	models, err := b.fetchModels()
	if err != nil {
		log.Printf("ListModels error: %v", err)
		b.replyf(msg, "commands.modelsError", escapeHTML(truncate(err.Error(), 300)))
		return
	}
	query = strings.ToLower(strings.TrimSpace(query))

	var sb strings.Builder
	n := 0
	for _, m := range models {
		if query == "" && !m.IsFree() {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.ID+" "+m.Name), query) {
			continue
		}
		if n == maxSearchResult {
			sb.WriteString("…\n")
			break
		}
		n++
		sb.WriteString("• <code>" + escapeHTML(m.ID) + "</code>")
		if query != "" {
			if p := m.PricePerMillion(); p != "" {
				sb.WriteString(" — " + escapeHTML(p))
			}
		}
		sb.WriteString("\n")
	}
	if n == 0 {
		b.reply(msg, b.t("commands.noModelsFound"))
		return
	}
	header := b.t("commands.getModels")
	if query != "" {
		header = fmt.Sprintf(b.t("commands.searchModels"), escapeHTML(query))
	}
	b.sendLongHTML(msg, header+sb.String())
}

func (b *Bot) setModelCommand(msg *tgbotapi.Message, arg string) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		b.showModelKeyboard(msg)
	case arg == "default" || arg == "reset":
		b.reply(msg, b.resetModel())
	case strings.ContainsAny(arg, " \t\n"):
		b.reply(msg, b.t("commands.noSpaceModel"))
	default:
		b.reply(msg, b.selectModel(arg, true))
	}
}

func (b *Bot) resetModel() string {
	if err := b.state.ResetModel(); err != nil {
		return fmt.Sprintf(b.t("commands.saveError"), escapeHTML(err.Error()))
	}
	conf := b.conf()
	log.Printf("Model reset to config default %s", conf.Model.ModelName)
	return fmt.Sprintf(b.t("commands.resetModel"), escapeHTML(conf.Model.ModelName))
}

// selectModel validates the id against the model list (when available) and
// stores it. It returns the HTML text to show to the admin.
func (b *Bot) selectModel(id string, validate bool) string {
	warning := ""
	if validate {
		models, err := b.fetchModels()
		if err != nil {
			// e.g. a local LLM server without /models: accept as is.
			warning = fmt.Sprintf(b.t("commands.modelUnverified"), escapeHTML(truncate(err.Error(), 200)))
		} else if exact, similar := findModel(models, id); exact == "" {
			text := fmt.Sprintf(b.t("commands.modelNotFound"), escapeHTML(id))
			if len(similar) > 0 {
				text += b.t("commands.modelSuggestions")
				for _, s := range similar {
					text += "• <code>" + escapeHTML(s) + "</code>\n"
				}
			}
			return text
		} else {
			id = exact
		}
	}
	if err := b.state.SetModel(id); err != nil {
		return fmt.Sprintf(b.t("commands.saveError"), escapeHTML(err.Error()))
	}
	log.Printf("Model changed to %s", id)
	return fmt.Sprintf(b.t("commands.setModel"), escapeHTML(id)) + warning
}

// findModel returns the exact (case-insensitive) match and similar ids.
func findModel(models []api.Model, id string) (string, []string) {
	want := strings.ToLower(id)
	short := want
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	short = strings.TrimSuffix(short, ":free")
	var similar []string
	for _, m := range models {
		lid := strings.ToLower(m.ID)
		if lid == want {
			return m.ID, nil
		}
		if len(similar) < maxSuggestions && short != "" && strings.Contains(lid, short) {
			similar = append(similar, m.ID)
		}
	}
	return "", similar
}

// modelChoices: config default, presets and recently used models.
func (b *Bot) modelChoices() []string {
	conf := b.conf()
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		if m != "" && !seen[m] && len(callbackModel+m) <= 64 {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range conf.ModelPresets {
		add(m)
	}
	for _, m := range b.state.Recent() {
		add(m)
	}
	return out
}

func (b *Bot) modelKeyboard() tgbotapi.InlineKeyboardMarkup {
	conf := b.conf()
	current := b.state.EffectiveModel(conf.Model.ModelName)
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, m := range b.modelChoices() {
		label := m
		if m == current {
			label = "✅ " + m
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, callbackModel+m)))
	}
	label := b.t("commands.defaultButton") + " (" + conf.Model.ModelName + ")"
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, callbackDefault)))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) showModelKeyboard(msg *tgbotapi.Message) {
	conf := b.conf()
	text := fmt.Sprintf(b.t("commands.chooseModel"), escapeHTML(b.state.EffectiveModel(conf.Model.ModelName)))
	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ParseMode = tgbotapi.ModeHTML
	m.ReplyToMessageID = msg.MessageID
	m.AllowSendingWithoutReply = true
	m.ReplyMarkup = b.modelKeyboard()
	if _, err := b.tg.Send(m); err != nil {
		log.Printf("Send model keyboard failed: %v", err)
	}
}

func (b *Bot) handleCallback(q *tgbotapi.CallbackQuery) {
	if !strings.HasPrefix(q.Data, callbackModel) {
		_, _ = b.tg.Request(tgbotapi.NewCallback(q.ID, ""))
		return
	}
	if q.From == nil || !b.conf().IsAdmin(q.From.ID) {
		_, _ = b.tg.Request(tgbotapi.NewCallbackWithAlert(q.ID, b.t("admin_only")))
		return
	}

	var text string
	if q.Data == callbackDefault {
		text = b.resetModel()
	} else {
		// Buttons only contain ids that were already accepted before.
		text = b.selectModel(strings.TrimPrefix(q.Data, callbackModel), false)
	}
	_, _ = b.tg.Request(tgbotapi.NewCallback(q.ID, ""))
	if q.Message != nil && q.Message.Chat != nil {
		edit := tgbotapi.NewEditMessageTextAndMarkup(q.Message.Chat.ID, q.Message.MessageID, text, b.modelKeyboard())
		edit.ParseMode = tgbotapi.ModeHTML
		if _, err := b.tg.Send(edit); err != nil {
			log.Printf("Edit model message failed: %v", err)
		}
	}
}
