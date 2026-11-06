package bot

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"

	"openrouter-bot/config"
	"openrouter-bot/state"
)

func TestPrivateMessageGetsReply(t *testing.T) {
	e := newEnv(t)
	e.llm.handler = func(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return answer("**Привет**!"), nil
	}
	m := e.private(userID, "Привет")

	sent := e.tg.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 message, got %v", e.texts())
	}
	if sent[0].Text != "<b>Привет</b>!" || sent[0].ParseMode != tgbotapi.ModeHTML {
		t.Errorf("unexpected reply %+v", sent[0])
	}
	if sent[0].ReplyTo != m.MessageID {
		t.Errorf("reply should quote the user message")
	}
	req := e.llm.Requests()[0]
	if req.Model != "default/model:free" {
		t.Errorf("model = %q", req.Model)
	}
	if req.Messages[0].Role != openai.ChatMessageRoleSystem || req.Messages[0].Content != e.conf.SystemPrompt {
		t.Errorf("system prompt missing: %+v", req.Messages[0])
	}
	if e.lastPrompt() != "Привет" {
		t.Errorf("prompt = %q", e.lastPrompt())
	}
}

func TestTypingIndicatorIsSent(t *testing.T) {
	e := newEnv(t)
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		time.Sleep(50 * time.Millisecond)
		return answer("ok"), nil
	}
	e.private(userID, "hi")
	typing := 0
	for _, r := range e.tg.Requests() {
		if a, ok := r.(tgbotapi.ChatActionConfig); ok && a.Action == tgbotapi.ChatTyping {
			typing++
		}
	}
	if typing < 2 {
		t.Errorf("typing should be refreshed while waiting, got %d", typing)
	}
}

func TestGroupAddressing(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantPrompt string // "" = must be ignored
	}{
		{"plain message ignored", "всем привет", ""},
		{"mention", "@" + botName + " сколько будет 2+2?", "сколько будет 2+2?"},
		{"mention case-insensitive", "@testopenrouterbot, привет", "привет"},
		{"mention in the middle", "скажи @" + botName + " который час", "скажи который час"},
		{"mention of another bot", "@" + botName + "_other привет", ""},
		{"short name at start", "Пирдун, как дела?", "как дела?"},
		{"short name upper case", "ПИРДУН расскажи анекдот", "расскажи анекдот"},
		{"short name inside", "эй бот ты тут?", "эй бот ты тут?"},
		{"short name as part of word", "пирдуны и роботы", ""},
		{"another alias as part of word", "работа ботаника", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(userID, tc.text)
			reqs := e.llm.Requests()
			if tc.wantPrompt == "" {
				if len(reqs) != 0 || len(e.tg.Sent()) != 0 {
					t.Fatalf("should be ignored, got LLM calls %d, sent %v", len(reqs), e.texts())
				}
				return
			}
			if len(reqs) != 1 {
				t.Fatalf("LLM calls = %d", len(reqs))
			}
			if got := e.lastPrompt(); got != tc.wantPrompt {
				t.Errorf("prompt = %q, want %q", got, tc.wantPrompt)
			}
			if len(e.tg.Sent()) != 1 {
				t.Errorf("want one reply, got %v", e.texts())
			}
		})
	}
}

func TestGroupReplyToBotMessage(t *testing.T) {
	e := newEnv(t)
	m := e.newMsg(groupChat(), userID, "а подробнее?")
	m.ReplyToMessage = &tgbotapi.Message{MessageID: 77, From: &tgbotapi.User{ID: botID, IsBot: true}, Text: "старый ответ"}
	e.handle(m)
	if got := e.lastPrompt(); got != "а подробнее?" {
		t.Errorf("prompt = %q (bot's own message must not be quoted)", got)
	}
}

func TestGroupStickerReplyToBotIsIgnored(t *testing.T) {
	e := newEnv(t)
	m := e.newMsg(groupChat(), userID, "")
	m.Sticker = &tgbotapi.Sticker{FileID: "s"}
	m.ReplyToMessage = &tgbotapi.Message{MessageID: 77, From: &tgbotapi.User{ID: botID, IsBot: true}, Text: "ответ"}
	e.handle(m)
	if len(e.llm.Requests()) != 0 || len(e.tg.Sent()) != 0 {
		t.Fatalf("sticker reply must be ignored: %v", e.texts())
	}
}

func TestPhotoCaptionCommandInGroup(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Vision = true })
	e.tg.fileURL = newFileServer(t, []byte("\xff\xd8\xff\xe0fakejpeg")).URL
	m := e.newMsg(groupChat(), userID, "")
	m.Caption = "/ask что на фото?"
	m.CaptionEntities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 4}}
	m.Photo = []tgbotapi.PhotoSize{{FileID: "p"}}
	e.handle(m)
	if got := e.lastPrompt(); got != "что на фото?" {
		t.Fatalf("prompt = %q", got)
	}
	parts := e.llm.Requests()[0].Messages[1].MultiContent
	if len(parts) != 2 || !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/jpeg;base64,") {
		t.Errorf("image not attached: %+v", parts)
	}
}

func TestGroupReplyToOtherUserIsIgnored(t *testing.T) {
	e := newEnv(t)
	m := e.newMsg(groupChat(), userID, "согласен")
	m.ReplyToMessage = &tgbotapi.Message{MessageID: 77, From: &tgbotapi.User{ID: 42}, Text: "текст"}
	e.handle(m)
	if len(e.llm.Requests()) != 0 {
		t.Fatal("reply to another user must not trigger the bot")
	}
}

func TestGroupCommands(t *testing.T) {
	for _, text := range []string{"/ask вопрос", "/ask@" + botName + " вопрос", "/ASK вопрос", "/pirdun вопрос"} {
		t.Run(text, func(t *testing.T) {
			e := newEnv(t)
			e.group(userID, text)
			if got := e.lastPrompt(); got != "вопрос" {
				t.Errorf("prompt = %q", got)
			}
		})
	}
}

func TestCommandForAnotherBotIgnored(t *testing.T) {
	e := newEnv(t)
	e.group(userID, "/ask@other_bot вопрос")
	e.group(userID, "/start@other_bot")
	if len(e.llm.Requests()) != 0 || len(e.tg.Sent()) != 0 {
		t.Fatalf("commands for other bots must be ignored: %v", e.texts())
	}
}

func TestAskAsReplyUsesQuotedMessage(t *testing.T) {
	e := newEnv(t)
	m := e.newMsg(groupChat(), userID, "/ask")
	m.ReplyToMessage = &tgbotapi.Message{MessageID: 5, From: &tgbotapi.User{ID: 42}, Text: "Земля плоская"}
	e.handle(m)
	if got := e.lastPrompt(); got != "Земля плоская" {
		t.Errorf("prompt = %q", got)
	}

	m = e.newMsg(groupChat(), userID, "/ask это правда?")
	m.ReplyToMessage = &tgbotapi.Message{MessageID: 5, From: &tgbotapi.User{ID: 42}, Text: "Земля плоская"}
	e.handle(m)
	got := e.lastPrompt()
	if !strings.Contains(got, "«Земля плоская»") || !strings.HasSuffix(got, "это правда?") {
		t.Errorf("prompt = %q", got)
	}
}

func TestAskWithoutArgumentsShowsUsage(t *testing.T) {
	e := newEnv(t)
	e.group(userID, "/ask")
	if len(e.llm.Requests()) != 0 {
		t.Fatal("LLM must not be called")
	}
	if txt := e.texts(); len(txt) != 1 || !strings.Contains(txt[0], "/ask") {
		t.Errorf("want usage, got %v", txt)
	}
}

func TestMentionWithoutTextAsksForQuestion(t *testing.T) {
	e := newEnv(t)
	e.group(userID, "@"+botName)
	if len(e.llm.Requests()) != 0 {
		t.Fatal("LLM must not be called with an empty prompt")
	}
	if txt := e.texts(); len(txt) != 1 || !strings.Contains(txt[0], "вопрос") {
		t.Errorf("got %v", txt)
	}
}

func TestServiceAndBotMessagesIgnored(t *testing.T) {
	e := newEnv(t)
	e.bot.HandleUpdate(tgbotapi.Update{Message: &tgbotapi.Message{Chat: privateChat(1)}}) // no From
	e.bot.HandleUpdate(tgbotapi.Update{Message: &tgbotapi.Message{
		From: &tgbotapi.User{ID: 9, IsBot: true}, Chat: privateChat(9), Text: "hi"}})
	e.bot.HandleUpdate(tgbotapi.Update{Message: &tgbotapi.Message{
		From: &tgbotapi.User{ID: 9}, Chat: &tgbotapi.Chat{ID: -1, Type: "channel"}, Text: "hi"}})
	m := e.newMsg(groupChat(), userID, "")
	m.NewChatMembers = []tgbotapi.User{{ID: botID}}
	e.handle(m)
	e.bot.HandleUpdate(tgbotapi.Update{}) // empty update
	e.bot.Wait()
	if len(e.llm.Requests()) != 0 || len(e.tg.Sent()) != 0 {
		t.Fatalf("nothing should be sent: %v", e.texts())
	}
}

func TestLongAnswerIsSplit(t *testing.T) {
	e := newEnv(t)
	var sb strings.Builder
	for i := 0; i < 400; i++ {
		sb.WriteString("Это довольно длинная строка ответа номер ")
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString("\n")
	}
	sb.WriteString("```go\n" + strings.Repeat("fmt.Println(\"hi\")\n", 300) + "```\n")
	long := sb.String()
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return answer(long), nil
	}
	m := e.private(userID, "дай длинный ответ")
	sent := e.tg.Sent()
	if len(sent) < 3 {
		t.Fatalf("want several messages, got %d", len(sent))
	}
	for i, s := range sent {
		if visibleLen(s) > TelegramLimit {
			t.Errorf("message %d too long: %d", i, visibleLen(s))
		}
		if s.ParseMode != tgbotapi.ModeHTML {
			t.Errorf("message %d was not sent as HTML", i)
		}
		if (i == 0) != (s.ReplyTo == m.MessageID) {
			t.Errorf("only the first chunk should be a reply (chunk %d reply=%d)", i, s.ReplyTo)
		}
		if strings.Count(s.Text, "<pre>") != strings.Count(s.Text, "</pre>") {
			t.Errorf("chunk %d has unbalanced <pre>", i)
		}
	}
}

func TestHTMLRejectedFallsBackToPlainText(t *testing.T) {
	e := newEnv(t)
	e.tg.failHTML = true
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return answer("**жирный** <b>"), nil
	}
	e.private(userID, "hi")
	sent := e.tg.Sent()
	if len(sent) != 1 || sent[0].ParseMode != "" || sent[0].Text != "**жирный** <b>" {
		t.Fatalf("want plain text fallback, got %+v", sent)
	}
}

func TestLLMErrorIsReported(t *testing.T) {
	e := newEnv(t)
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return openai.ChatCompletionResponse{}, errors.New("429 rate limited <upstream>")
	}
	e.private(userID, "hi")
	e.private(adminID, "hi")
	txt := e.texts()
	if len(txt) != 2 {
		t.Fatalf("got %v", txt)
	}
	if txt[0] != "Ошибка обработки запроса" {
		t.Errorf("user should get a generic error, got %q", txt[0])
	}
	if !strings.Contains(txt[1], "429 rate limited &lt;upstream&gt;") {
		t.Errorf("admin should see escaped details, got %q", txt[1])
	}
}

func TestEmptyAnswerDoesNotPanic(t *testing.T) {
	for name, resp := range map[string]openai.ChatCompletionResponse{
		"no choices":    {ID: "x"},
		"empty content": answer("   "),
		"only thinking": answer("<think>hmm</think>"),
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
				return resp, nil
			}
			e.private(userID, "hi")
			txt := e.texts()
			if len(txt) != 1 || !strings.Contains(txt[0], "пустой ответ") {
				t.Errorf("got %v", txt)
			}
		})
	}
}

func TestThinkingIsStripped(t *testing.T) {
	e := newEnv(t)
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return answer("<think>секрет</think>\nОтвет"), nil
	}
	e.private(userID, "hi")
	if txt := e.texts(); len(txt) != 1 || txt[0] != "Ответ" {
		t.Errorf("got %v", txt)
	}
}

func TestPanicInHandlerIsRecovered(t *testing.T) {
	e := newEnv(t)
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		panic("boom")
	}
	e.private(userID, "hi")
	e.llm.handler = nil
	e.private(userID, "hi again")
	if txt := e.texts(); len(txt) != 1 || txt[0] != "ok" {
		t.Errorf("bot should keep working after a panic, got %v", txt)
	}
}

func TestRequestTimeout(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RequestTimeout = 30 * time.Millisecond })
	e.llm.handler = func(ctx context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		<-ctx.Done()
		return openai.ChatCompletionResponse{}, ctx.Err()
	}
	e.private(userID, "hi")
	if txt := e.texts(); len(txt) != 1 || !strings.Contains(txt[0], "вовремя") {
		t.Errorf("got %v", txt)
	}
}

func TestStopCancelsActiveRequest(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{})
	e.llm.handler = func(ctx context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		close(started)
		<-ctx.Done()
		return openai.ChatCompletionResponse{}, ctx.Err()
	}
	e.bot.HandleUpdate(tgbotapi.Update{Message: e.newMsg(privateChat(userID), userID, "долгий вопрос")})
	<-started
	e.private(userID, "/stop")
	e.private(userID, "/stop")
	txt := e.texts()
	if len(txt) != 2 || txt[0] != "Запрос остановлен." || txt[1] != "Нет активного запроса." {
		t.Errorf("got %v", txt)
	}
}

func TestBudget(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.GuestBudget = 0 })
	e.private(guestID, "hi")
	if len(e.llm.Requests()) != 0 {
		t.Fatal("guest without budget must not reach the LLM")
	}
	if txt := e.texts(); len(txt) != 1 || !strings.Contains(txt[0], "бюджет") {
		t.Errorf("got %v", txt)
	}

	// Allowed user over budget.
	e.users.GetUser(userID, "").AddCost(2)
	e.private(userID, "hi")
	if len(e.llm.Requests()) != 0 {
		t.Fatal("user over budget must not reach the LLM")
	}
	// Admin is never limited.
	e.users.GetUser(adminID, "").AddCost(100)
	e.private(adminID, "hi")
	if len(e.llm.Requests()) != 1 {
		t.Fatal("admin must have access")
	}
}

func TestCostIsRecorded(t *testing.T) {
	e := newEnv(t)
	e.catalog.cost = 0.25
	e.private(userID, "hi")
	if got := e.users.GetUser(userID, "").GetCurrentCost("total"); got != 0.25 {
		t.Errorf("cost = %v", got)
	}
	if len(e.catalog.costIDs) != 1 || e.catalog.costIDs[0] != "gen-1" {
		t.Errorf("cost ids = %v", e.catalog.costIDs)
	}
}

func TestHistoryIsPerChatAndUser(t *testing.T) {
	e := newEnv(t)
	var n int32
	e.llm.handler = func(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		return answer("ответ " + string(rune('0'+atomic.AddInt32(&n, 1)))), nil
	}
	e.private(userID, "первый")
	e.private(userID, "второй")
	reqs := e.llm.Requests()
	msgs := reqs[1].Messages
	if len(msgs) != 4 || msgs[1].Content != "первый" || msgs[2].Content != "ответ 1" || msgs[3].Content != "второй" {
		t.Errorf("history not sent: %+v", msgs)
	}

	e.group(userID, "/ask третий") // other chat: fresh history
	if msgs := e.llm.Requests()[2].Messages; len(msgs) != 2 {
		t.Errorf("group chat must not see the private history: %+v", msgs)
	}

	e.private(userID, "/reset")
	e.private(userID, "четвёртый")
	if msgs := e.llm.Requests()[3].Messages; len(msgs) != 2 {
		t.Errorf("history must be empty after /reset: %+v", msgs)
	}
}

func TestResetWithCustomPrompt(t *testing.T) {
	e := newEnv(t)
	e.private(userID, "/reset Ты пират")
	e.private(userID, "привет")
	if got := e.llm.Requests()[0].Messages[0].Content; got != "Ты пират" {
		t.Errorf("system prompt = %q", got)
	}
	e.private(userID, "/reset system")
	e.private(userID, "привет")
	if got := e.llm.Requests()[1].Messages[0].Content; got != e.conf.SystemPrompt {
		t.Errorf("system prompt = %q", got)
	}
}

func TestStats(t *testing.T) {
	e := newEnv(t)
	e.private(userID, "/stats")
	e.private(adminID, "/stats")
	txt := e.texts()
	if len(txt) != 2 || !strings.Contains(txt[0], "только администраторам") || !strings.Contains(txt[1], "Статистика") {
		t.Errorf("got %v", txt)
	}
}

func TestHelpDependsOnRole(t *testing.T) {
	e := newEnv(t)
	e.private(userID, "/help")
	e.private(adminID, "/help")
	txt := e.texts()
	if strings.Contains(txt[0], "/set_model") {
		t.Error("user help must not list admin commands")
	}
	if !strings.Contains(txt[1], "/set_model") {
		t.Error("admin help must list admin commands")
	}
}

func TestStartInGroupExplainsAddressing(t *testing.T) {
	e := newEnv(t)
	e.group(userID, "/start")
	if txt := e.texts(); len(txt) != 1 || !strings.Contains(txt[0], "@"+botName) {
		t.Errorf("got %v", txt)
	}
}

func TestRegisterCommands(t *testing.T) {
	e := newEnv(t)
	e.bot.RegisterCommands()
	var def, admin *tgbotapi.SetMyCommandsConfig
	for _, r := range e.tg.Requests() {
		c, ok := r.(tgbotapi.SetMyCommandsConfig)
		if !ok {
			continue
		}
		if c.Scope == nil {
			def = &c
		} else if c.Scope.Type == "chat" && c.Scope.ChatID == adminID {
			admin = &c
		}
	}
	has := func(c *tgbotapi.SetMyCommandsConfig, name string) bool {
		for _, cmd := range c.Commands {
			if cmd.Command == name {
				return true
			}
		}
		return false
	}
	if def == nil || admin == nil {
		t.Fatalf("default=%v admin=%v", def, admin)
	}
	if has(def, "set_model") || !has(def, "ask") {
		t.Errorf("default commands: %+v", def.Commands)
	}
	if !has(admin, "set_model") || !has(admin, "model") {
		t.Errorf("admin commands: %+v", admin.Commands)
	}
}

func TestVisionSendsImageAsDataURL(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000fakepng")
	e := newEnv(t, func(c *config.Config) { c.Vision = true })
	e.tg.fileURL = newFileServer(t, png).URL

	m := e.newMsg(privateChat(userID), userID, "")
	m.Photo = []tgbotapi.PhotoSize{{FileID: "small"}, {FileID: "big"}}
	e.handle(m)

	req := e.llm.Requests()
	if len(req) != 1 {
		t.Fatalf("LLM calls = %d (sent %v)", len(req), e.texts())
	}
	parts := req[0].Messages[len(req[0].Messages)-1].MultiContent
	if len(parts) != 2 || parts[0].Text != "Describe the image" {
		t.Fatalf("parts = %+v", parts)
	}
	url := parts[1].ImageURL.URL
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("image must be inlined, got %.60s", url)
	}
	if strings.Contains(url, "SECRET") {
		t.Error("bot token leaked to the LLM provider")
	}
}

func TestVisionDisabledUsesCaption(t *testing.T) {
	e := newEnv(t)
	m := e.newMsg(privateChat(userID), userID, "")
	m.Caption = "что это?"
	m.Photo = []tgbotapi.PhotoSize{{FileID: "x"}}
	e.handle(m)
	if got := e.lastPrompt(); got != "что это?" {
		t.Errorf("prompt = %q", got)
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	e := newEnv(t)
	e.private(adminID, "/set_model openai/gpt-4o-mini")
	store, err := state.Open(e.dir + "/bot_state.json")
	if err != nil {
		t.Fatal(err)
	}
	if store.Model() != "openai/gpt-4o-mini" {
		t.Errorf("model after restart = %q", store.Model())
	}
}
