// Package bot contains the Telegram update handling logic. It talks to
// Telegram and the LLM through small interfaces so it can be tested offline.
package bot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"

	"openrouter-bot/api"
	"openrouter-bot/config"
	"openrouter-bot/lang"
	"openrouter-bot/state"
	"openrouter-bot/user"
)

// Telegram is the subset of *tgbotapi.BotAPI used by the bot.
type Telegram interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetFileDirectURL(fileID string) (string, error)
}

// LLM is the subset of *openai.Client used by the bot.
type LLM interface {
	CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// Catalog lists models and looks up generation costs (*api.Client).
type Catalog interface {
	ListModels(ctx context.Context) ([]api.Model, error)
	GenerationCost(ctx context.Context, id string) (float64, error)
}

type Options struct {
	Telegram Telegram
	// Self is the bot account (from getMe): used to detect mentions/replies.
	Self    tgbotapi.User
	LLM     LLM
	Catalog Catalog
	Config  func() *config.Config
	State   *state.Store
	Users   *user.Manager
	// HTTPClient downloads images for vision requests.
	HTTPClient     *http.Client
	TypingInterval time.Duration
}

type Bot struct {
	tg      Telegram
	self    tgbotapi.User
	llm     LLM
	catalog Catalog
	conf    func() *config.Config
	state   *state.Store
	users   *user.Manager
	http    *http.Client
	typing  time.Duration
	wg      sync.WaitGroup
}

func New(o Options) *Bot {
	b := &Bot{
		tg: o.Telegram, self: o.Self, llm: o.LLM, catalog: o.Catalog,
		conf: o.Config, state: o.State, users: o.Users, http: o.HTTPClient,
		typing: o.TypingInterval,
	}
	if b.state == nil {
		b.state, _ = state.Open("")
	}
	if b.users == nil {
		b.users = user.NewUserManager("")
	}
	if b.http == nil {
		b.http = &http.Client{Timeout: 30 * time.Second}
	}
	if b.typing <= 0 {
		b.typing = 4 * time.Second
	}
	return b
}

// Wait blocks until all background work (LLM requests etc.) has finished.
func (b *Bot) Wait() { b.wg.Wait() }

// goSafe runs fn in a goroutine; a panic is logged instead of killing the bot.
func (b *Bot) goSafe(fn func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer recoverPanic()
		fn()
	}()
}

func recoverPanic() {
	if r := recover(); r != nil {
		log.Printf("PANIC recovered: %v\n%s", r, debug.Stack())
	}
}

func (b *Bot) t(key string) string { return lang.Translate(key, b.conf().Lang) }

// HandleUpdate processes one update. It never blocks on the LLM.
func (b *Bot) HandleUpdate(u tgbotapi.Update) {
	defer recoverPanic()
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(u.CallbackQuery)
	case u.Message != nil:
		b.handleMessage(u.Message)
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	if msg.From == nil || msg.Chat == nil || msg.From.IsBot {
		return
	}
	if !(msg.Chat.IsPrivate() || msg.Chat.IsGroup() || msg.Chat.IsSuperGroup()) {
		return
	}
	if msg.Text == "" && msg.Caption != "" {
		// A photo with a caption: treat the caption as the text so that
		// "/ask what is this?" or "@bot ..." in a caption work too.
		cp := *msg
		cp.Text, cp.Entities, cp.Caption = msg.Caption, msg.CaptionEntities, ""
		msg = &cp
	}

	if msg.IsCommand() {
		cmd, ok := b.commandFor(msg)
		if !ok {
			return // command for another bot, e.g. /ask@other_bot
		}
		b.logMessage(msg, msg.Text)
		b.handleCommand(msg, cmd)
		return
	}

	text, ok := b.addressedText(msg)
	if !ok {
		return
	}
	b.logMessage(msg, text)
	b.goSafe(func() { b.ask(msg, text) })
}

func (b *Bot) logMessage(msg *tgbotapi.Message, text string) {
	if len([]rune(text)) > 100 {
		text = string([]rune(text)[:100]) + "…"
	}
	log.Printf("[%s %d] @%s (id %d): %q", msg.Chat.Type, msg.Chat.ID, msg.From.UserName, msg.From.ID, text)
}

// commandFor returns the lower-cased command name if it is meant for this bot.
func (b *Bot) commandFor(msg *tgbotapi.Message) (string, bool) {
	full := msg.CommandWithAt()
	if i := strings.Index(full, "@"); i >= 0 {
		if !strings.EqualFold(full[i+1:], b.self.UserName) {
			return "", false
		}
		full = full[:i]
	}
	return strings.ToLower(full), full != ""
}

// reply sends an HTML message as a reply to msg.
func (b *Bot) reply(msg *tgbotapi.Message, html string) {
	m := tgbotapi.NewMessage(msg.Chat.ID, html)
	m.ParseMode = tgbotapi.ModeHTML
	m.ReplyToMessageID = msg.MessageID
	m.AllowSendingWithoutReply = true
	m.DisableWebPagePreview = true
	if _, err := b.tg.Send(m); err != nil {
		log.Printf("Send to %d failed: %v", msg.Chat.ID, err)
	}
}

func (b *Bot) replyf(msg *tgbotapi.Message, key string, args ...interface{}) {
	b.reply(msg, fmt.Sprintf(b.t(key), args...))
}

// sendLongHTML sends text (already HTML) split into several messages if needed.
func (b *Bot) sendLongHTML(msg *tgbotapi.Message, html string) {
	for i, chunk := range splitMessage(html, chunkLimit) {
		m := tgbotapi.NewMessage(msg.Chat.ID, chunk)
		m.ParseMode = tgbotapi.ModeHTML
		m.DisableWebPagePreview = true
		if i == 0 {
			m.ReplyToMessageID = msg.MessageID
			m.AllowSendingWithoutReply = true
		}
		if _, err := b.tg.Send(m); err != nil {
			log.Printf("Send to %d failed: %v", msg.Chat.ID, err)
		}
	}
}
