package bot

import (
	"errors"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestAdminCommandsDeniedForOthers(t *testing.T) {
	for _, cmd := range []string{"/set_model openai/gpt-4o-mini", "/set_model", "/get_models", "/model"} {
		for _, from := range []int64{userID, guestID} {
			e := newEnv(t)
			e.private(from, cmd)
			e.group(from, cmd)
			for _, txt := range e.texts() {
				if txt != "Эта команда доступна только администраторам." {
					t.Errorf("%s from %d: got %q", cmd, from, txt)
				}
			}
			if e.store.Model() != "" {
				t.Errorf("%s from %d changed the model", cmd, from)
			}
		}
	}
}

func TestSetModelByAdmin(t *testing.T) {
	e := newEnv(t)
	e.private(adminID, "/set_model openai/GPT-4o-mini")
	if got := e.store.Model(); got != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q (id should be normalised to the catalog spelling)", got)
	}
	if txt := e.texts(); !strings.Contains(txt[0], "<code>openai/gpt-4o-mini</code>") {
		t.Errorf("got %v", txt)
	}

	// The new model is used by everybody, including other chats.
	e.group(userID, "/ask привет")
	if got := e.llm.Requests()[0].Model; got != "openai/gpt-4o-mini" {
		t.Errorf("request model = %q", got)
	}

	e.private(adminID, "/model")
	if txt := e.texts(); !strings.Contains(txt[len(txt)-1], "openai/gpt-4o-mini") ||
		!strings.Contains(txt[len(txt)-1], "default/model:free") {
		t.Errorf("/model: %q", txt[len(txt)-1])
	}

	e.private(adminID, "/set_model default")
	if e.store.Model() != "" {
		t.Error("default should clear the override")
	}
	e.private(userID, "hi")
	if got := e.llm.Requests()[1].Model; got != "default/model:free" {
		t.Errorf("request model = %q", got)
	}
}

func TestSetModelUnknownSuggestsSimilar(t *testing.T) {
	e := newEnv(t)
	e.private(adminID, "/set_model deepseek/deepseek-chat-v3-0324:fre")
	if e.store.Model() != "" {
		t.Fatal("unknown model must not be applied")
	}
	txt := e.texts()[0]
	if !strings.Contains(txt, "не найдена") || !strings.Contains(txt, "deepseek/deepseek-chat-v3-0324:free") {
		t.Errorf("got %q", txt)
	}
}

func TestSetModelWithSpaces(t *testing.T) {
	e := newEnv(t)
	e.private(adminID, "/set_model a b")
	if e.store.Model() != "" || !strings.Contains(e.texts()[0], "пробелы") {
		t.Errorf("got %v", e.texts())
	}
}

func TestSetModelWhenCatalogUnavailable(t *testing.T) {
	e := newEnv(t)
	e.catalog.err = errors.New("404 no /models on local server")
	e.private(adminID, "/set_model local-llama")
	if e.store.Model() != "local-llama" {
		t.Fatal("model should be accepted when the list can't be fetched")
	}
	if txt := e.texts()[0]; !strings.Contains(txt, "⚠") {
		t.Errorf("a warning is expected, got %q", txt)
	}
}

func keyboardData(t *testing.T, markup interface{}) []string {
	t.Helper()
	kb, ok := markup.(tgbotapi.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("no inline keyboard: %#v", markup)
	}
	var data []string
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			data = append(data, *b.CallbackData)
		}
	}
	return data
}

func TestSetModelKeyboardAndCallbacks(t *testing.T) {
	e := newEnv(t)
	e.private(adminID, "/set_model openai/gpt-4o-mini") // goes to "recent"
	e.private(adminID, "/set_model")
	sent := e.tg.Sent()
	data := keyboardData(t, sent[len(sent)-1].Markup)
	want := []string{"m:preset/one:free", "m:preset/two", "m:openai/gpt-4o-mini", "m:-"}
	if strings.Join(data, ",") != strings.Join(want, ",") {
		t.Errorf("buttons = %v, want %v", data, want)
	}

	kbMsg := &tgbotapi.Message{MessageID: 99, Chat: privateChat(adminID)}
	callback := func(from int64, data string) {
		e.bot.HandleUpdate(tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
			ID: "cb", From: &tgbotapi.User{ID: from}, Message: kbMsg, Data: data}})
		e.bot.Wait()
	}

	// Somebody else presses the button (e.g. in a group).
	callback(userID, "m:preset/two")
	if e.store.Model() != "openai/gpt-4o-mini" {
		t.Fatal("non-admin changed the model via callback")
	}
	last := e.tg.Requests()[len(e.tg.Requests())-1].(tgbotapi.CallbackConfig)
	if !last.ShowAlert || !strings.Contains(last.Text, "администратор") {
		t.Errorf("non-admin should get an alert, got %+v", last)
	}

	callback(adminID, "m:preset/two")
	if e.store.Model() != "preset/two" {
		t.Fatalf("model = %q", e.store.Model())
	}
	sent = e.tg.Sent()
	edit := sent[len(sent)-1]
	if !edit.Edit || !strings.Contains(edit.Text, "preset/two") {
		t.Errorf("message should be edited, got %+v", edit)
	}

	callback(adminID, "m:-")
	if e.store.Model() != "" {
		t.Errorf("default button should reset, got %q", e.store.Model())
	}
}

func TestGetModels(t *testing.T) {
	e := newEnv(t)
	e.private(adminID, "/get_models")
	txt := e.texts()[0]
	if !strings.Contains(txt, "deepseek/deepseek-chat-v3-0324:free") || strings.Contains(txt, "gpt-4o-mini") {
		t.Errorf("free list: %q", txt)
	}

	e.private(adminID, "/get_models GPT")
	txt = e.texts()[1]
	if !strings.Contains(txt, "openai/gpt-4o-mini</code> — $0.15/$0.6") {
		t.Errorf("search: %q", txt)
	}

	e.private(adminID, "/get_models nothing-like-this")
	if txt := e.texts()[2]; !strings.Contains(txt, "не найдены") {
		t.Errorf("empty search: %q", txt)
	}

	e.catalog.err = errors.New("boom")
	e.private(adminID, "/get_models")
	if txt := e.texts()[3]; !strings.Contains(txt, "boom") {
		t.Errorf("error: %q", txt)
	}
}

func TestGetModelsLongListIsSplit(t *testing.T) {
	e := newEnv(t)
	e.catalog.models = nil
	for i := 0; i < 300; i++ {
		e.catalog.models = append(e.catalog.models,
			model("vendor/some-really-long-model-name-number-"+strings.Repeat("x", i%10)+string(rune('a'+i%26))+":free", "0", "0"))
	}
	e.private(adminID, "/get_models")
	for _, s := range e.tg.Sent() {
		if visibleLen(s) > TelegramLimit {
			t.Fatalf("message too long: %d", visibleLen(s))
		}
	}
}
