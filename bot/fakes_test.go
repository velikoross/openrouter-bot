package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"

	"openrouter-bot/api"
	"openrouter-bot/config"
	"openrouter-bot/state"
	"openrouter-bot/user"
)

const (
	botID    int64 = 1000
	botName        = "TestOpenRouterBot"
	adminID  int64 = 1
	userID   int64 = 2
	guestID  int64 = 3
	groupID  int64 = -100500
	botToken       = "123:SECRET-TOKEN"
)

// sent is a message the bot sent to Telegram.
type sent struct {
	ChatID    int64
	Text      string
	ParseMode string
	ReplyTo   int
	Markup    interface{}
	Edit      bool
}

type fakeTelegram struct {
	mu       sync.Mutex
	sent     []sent
	requests []tgbotapi.Chattable
	failHTML bool
	fileURL  string
	nextID   int
}

func (f *fakeTelegram) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s sent
	switch m := c.(type) {
	case tgbotapi.MessageConfig:
		s = sent{ChatID: m.ChatID, Text: m.Text, ParseMode: m.ParseMode, ReplyTo: m.ReplyToMessageID, Markup: m.ReplyMarkup}
	case tgbotapi.EditMessageTextConfig:
		s = sent{ChatID: m.ChatID, Text: m.Text, ParseMode: m.ParseMode, Edit: true}
		if m.ReplyMarkup != nil {
			s.Markup = *m.ReplyMarkup
		}
	default:
		return tgbotapi.Message{}, errors.New("unexpected chattable")
	}
	if visibleLen(s) > TelegramLimit {
		return tgbotapi.Message{}, errors.New("Bad Request: message is too long")
	}
	if s.Text == "" {
		return tgbotapi.Message{}, errors.New("Bad Request: message text is empty")
	}
	if f.failHTML && s.ParseMode == tgbotapi.ModeHTML {
		return tgbotapi.Message{}, errors.New("Bad Request: can't parse entities")
	}
	f.sent = append(f.sent, s)
	f.nextID++
	return tgbotapi.Message{MessageID: f.nextID}, nil
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// visibleLen is how Telegram counts the limit: after entities parsing.
func visibleLen(s sent) int {
	if s.ParseMode != tgbotapi.ModeHTML {
		return utf16Len(s.Text)
	}
	return utf16Len(html.UnescapeString(tagRe.ReplaceAllString(s.Text, "")))
}

func (f *fakeTelegram) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, c)
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (f *fakeTelegram) GetFileDirectURL(fileID string) (string, error) {
	return f.fileURL + "/file/bot" + botToken + "/" + fileID, nil
}

func (f *fakeTelegram) Sent() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.sent...)
}

func (f *fakeTelegram) Requests() []tgbotapi.Chattable {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tgbotapi.Chattable(nil), f.requests...)
}

type fakeLLM struct {
	mu       sync.Mutex
	requests []openai.ChatCompletionRequest
	handler  func(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

func (f *fakeLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	h := f.handler
	f.mu.Unlock()
	if h == nil {
		return answer("ok"), nil
	}
	return h(ctx, req)
}

func (f *fakeLLM) Requests() []openai.ChatCompletionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]openai.ChatCompletionRequest(nil), f.requests...)
}

func answer(text string) openai.ChatCompletionResponse {
	return openai.ChatCompletionResponse{
		ID: "gen-1",
		Choices: []openai.ChatCompletionChoice{{
			Message:      openai.ChatCompletionMessage{Role: "assistant", Content: text},
			FinishReason: openai.FinishReasonStop,
		}},
	}
}

type fakeCatalog struct {
	mu      sync.Mutex
	models  []api.Model
	err     error
	cost    float64
	costIDs []string
}

func (f *fakeCatalog) ListModels(context.Context) ([]api.Model, error) {
	return f.models, f.err
}

func (f *fakeCatalog) GenerationCost(_ context.Context, id string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.costIDs = append(f.costIDs, id)
	return f.cost, nil
}

func model(id, prompt, completion string) api.Model {
	m := api.Model{ID: id}
	m.Pricing.Prompt = prompt
	m.Pricing.Completion = completion
	return m
}

type env struct {
	t       *testing.T
	bot     *Bot
	tg      *fakeTelegram
	llm     *fakeLLM
	catalog *fakeCatalog
	conf    *config.Config
	store   *state.Store
	users   *user.Manager
	dir     string
	msgID   int
}

func baseConfig() *config.Config {
	return &config.Config{
		TelegramBotToken:   botToken,
		Model:              config.ModelParameters{Type: "openrouter", ModelName: "default/model:free", Temperature: 0.7, TopP: 0.7},
		MaxTokens:          100,
		OpenAIBaseURL:      "http://llm.invalid",
		SystemPrompt:       "Always answer in russian language.",
		BudgetPeriod:       "monthly",
		GuestBudget:        1,
		UserBudget:         1,
		AdminChatIDs:       []int64{adminID},
		AllowedUserChatIDs: []int64{userID},
		MaxHistorySize:     10,
		MaxHistoryTime:     60,
		VisionPrompt:       "Describe the image",
		VisionDetails:      "low",
		StatsMinRole:       "ADMIN",
		Lang:               "RU",
		BotAliases:         []string{"пирдун", "бот"},
		ModelPresets:       []string{"preset/one:free", "preset/two"},
		RequestTimeout:     5 * time.Second,
	}
}

func newEnv(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	conf := baseConfig()
	for _, m := range mutate {
		m(conf)
	}
	dir := t.TempDir()
	store, err := state.Open(dir + "/bot_state.json")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t:     t,
		tg:    &fakeTelegram{},
		llm:   &fakeLLM{},
		conf:  conf,
		store: store,
		users: user.NewUserManager(dir),
		dir:   dir,
		catalog: &fakeCatalog{models: []api.Model{
			model("default/model:free", "0", "0"),
			model("deepseek/deepseek-chat-v3-0324:free", "0", "0"),
			model("deepseek/deepseek-chat-v3-0324", "0.0000003", "0.0000008"),
			model("openai/gpt-4o-mini", "0.00000015", "0.0000006"),
		}},
	}
	e.bot = New(Options{
		Telegram:       e.tg,
		Self:           tgbotapi.User{ID: botID, IsBot: true, UserName: botName},
		LLM:            e.llm,
		Catalog:        e.catalog,
		Config:         func() *config.Config { return e.conf },
		State:          store,
		Users:          e.users,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		TypingInterval: 10 * time.Millisecond,
	})
	return e
}

func (e *env) newMsg(chat *tgbotapi.Chat, from int64, text string) *tgbotapi.Message {
	e.msgID++
	m := &tgbotapi.Message{
		MessageID: e.msgID,
		From:      &tgbotapi.User{ID: from, UserName: fmt.Sprintf("user%d", from)},
		Chat:      chat,
		Text:      text,
	}
	if strings.HasPrefix(text, "/") {
		end := strings.IndexAny(text, " \n")
		if end < 0 {
			end = len(text)
		}
		m.Entities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: end}}
	}
	return m
}

func privateChat(id int64) *tgbotapi.Chat { return &tgbotapi.Chat{ID: id, Type: "private"} }
func groupChat() *tgbotapi.Chat           { return &tgbotapi.Chat{ID: groupID, Type: "supergroup"} }

// private sends a private message from user `from` and waits for processing.
func (e *env) private(from int64, text string) *tgbotapi.Message {
	m := e.newMsg(privateChat(from), from, text)
	e.handle(m)
	return m
}

func (e *env) group(from int64, text string) *tgbotapi.Message {
	m := e.newMsg(groupChat(), from, text)
	e.handle(m)
	return m
}

func (e *env) handle(m *tgbotapi.Message) {
	e.bot.HandleUpdate(tgbotapi.Update{Message: m})
	e.bot.Wait()
}

// texts returns the texts of sent messages (without chat actions).
func (e *env) texts() []string {
	var out []string
	for _, s := range e.tg.Sent() {
		out = append(out, s.Text)
	}
	return out
}

func (e *env) lastPrompt() string {
	reqs := e.llm.Requests()
	if len(reqs) == 0 {
		e.t.Fatalf("LLM was not called")
	}
	msgs := reqs[len(reqs)-1].Messages
	last := msgs[len(msgs)-1]
	if last.Content != "" {
		return last.Content
	}
	for _, p := range last.MultiContent {
		if p.Type == openai.ChatMessagePartTypeText {
			return p.Text
		}
	}
	return ""
}

func newFileServer(t *testing.T, body []byte) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, botToken) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
