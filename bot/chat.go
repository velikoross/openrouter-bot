package bot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"

	"openrouter-bot/config"
	"openrouter-bot/user"
)

const maxImageBytes = 10 << 20

// quotedText returns the text of the message being replied to, unless it is
// the bot's own message (that one is already in the history).
func (b *Bot) quotedText(msg *tgbotapi.Message) string {
	r := msg.ReplyToMessage
	if r == nil || (r.From != nil && r.From.ID == b.self.ID) {
		return ""
	}
	t := r.Text
	if t == "" {
		t = r.Caption
	}
	return strings.TrimSpace(t)
}

func pickPhoto(msg *tgbotapi.Message) []tgbotapi.PhotoSize {
	if len(msg.Photo) > 0 {
		return msg.Photo
	}
	if msg.ReplyToMessage != nil {
		return msg.ReplyToMessage.Photo
	}
	return nil
}

// imageDataURL downloads a Telegram photo and returns it as a data: URL.
// The Telegram file URL contains the bot token, so it must never be sent to
// the LLM provider.
func (b *Bot) imageDataURL(ctx context.Context, photos []tgbotapi.PhotoSize) (string, error) {
	p := photos[len(photos)-1]
	link, err := b.tg.GetFileDirectURL(p.FileID)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return "", errors.New("download failed") // do not log the URL with the token
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxImageBytes {
		return "", errors.New("image is too large")
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// startTyping shows "typing…" until the returned func is called.
func (b *Bot) startTyping(ctx context.Context, chatID int64) func() {
	ctx, cancel := context.WithCancel(ctx)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		t := time.NewTicker(b.typing)
		defer t.Stop()
		for {
			_, _ = b.tg.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return cancel
}

// ask sends prompt to the LLM and replies with the answer.
func (b *Bot) ask(msg *tgbotapi.Message, prompt string) {
	conf := b.conf()
	from := msg.From
	usage := b.users.GetUser(from.ID, from.UserName)
	if !usage.HaveAccess(conf) {
		b.reply(msg, b.t("budget_out"))
		return
	}

	quoted := b.quotedText(msg)
	conv := b.users.Conversation(msg.Chat.ID, from.ID)
	conv.Lock()
	defer conv.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), conf.RequestTimeout)
	done := conv.StartRequest(cancel)
	defer done()

	var imageURL string
	if photos := pickPhoto(msg); conf.Vision && len(photos) > 0 {
		var err error
		if imageURL, err = b.imageDataURL(ctx, photos); err != nil {
			log.Printf("Image download for chat %d failed: %v", msg.Chat.ID, err)
		}
	}
	if prompt == "" && imageURL != "" {
		prompt = conf.VisionPrompt
	}
	if prompt == "" && quoted != "" {
		prompt, quoted = quoted, ""
	}
	if prompt == "" {
		b.reply(msg, b.t("emptyPrompt"))
		return
	}
	userText := prompt
	if quoted != "" {
		userText = fmt.Sprintf(b.t("quote"), quoted) + "\n\n" + prompt
	}

	stopTyping := b.startTyping(ctx, msg.Chat.ID)
	defer stopTyping()

	req := b.buildRequest(conf, conv.SystemPrompt(conf.SystemPrompt),
		conv.Messages(conf.MaxHistorySize, conf.MaxHistoryTime), userText, imageURL)

	resp, err := b.llm.CreateChatCompletion(ctx, req)
	stopTyping()
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return // stopped with /stop, the user already got a reply
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			b.reply(msg, b.t("timeoutText"))
		default:
			log.Printf("ChatCompletion error (model %s): %v", req.Model, err)
			text := b.t("errorText")
			if conf.IsAdmin(from.ID) {
				text += ": " + escapeHTML(truncate(err.Error(), 300))
			}
			b.reply(msg, text)
		}
		return
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return // stopped while the answer was arriving
	}

	answer := ""
	if len(resp.Choices) > 0 {
		answer = cleanAnswer(resp.Choices[0].Message.Content)
	}
	if answer == "" {
		reason := ""
		if len(resp.Choices) > 0 {
			reason = string(resp.Choices[0].FinishReason)
		}
		log.Printf("Empty answer from %s (finish_reason=%q)", req.Model, reason)
		b.reply(msg, b.t("emptyAnswer"))
		return
	}

	conv.AddExchange(userText, answer, conf.MaxHistorySize)
	b.sendAnswer(msg, answer)

	if conf.Model.Type == "openrouter" && resp.ID != "" && b.catalog != nil {
		id := resp.ID
		b.goSafe(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cost, err := b.catalog.GenerationCost(ctx, id)
			if err != nil {
				log.Printf("Cost lookup for %s failed: %v", id, err)
				return
			}
			usage.AddCost(cost)
		})
	}
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (b *Bot) buildRequest(conf *config.Config, system string, history []user.Message, text, imageURL string) openai.ChatCompletionRequest {
	messages := make([]openai.ChatCompletionMessage, 0, len(history)+2)
	if system != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: system})
	}
	for _, m := range history {
		messages = append(messages, openai.ChatCompletionMessage{Role: m.Role, Content: m.Content})
	}
	last := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: text}
	if imageURL != "" {
		last = openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeText, Text: text},
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{
					URL: imageURL, Detail: openai.ImageURLDetail(conf.VisionDetails),
				}},
			},
		}
	}
	messages = append(messages, last)

	return openai.ChatCompletionRequest{
		Model:            b.state.EffectiveModel(conf.Model.ModelName),
		Messages:         messages,
		MaxTokens:        conf.MaxTokens,
		Temperature:      float32(conf.Model.Temperature),
		TopP:             float32(conf.Model.TopP),
		FrequencyPenalty: float32(conf.Model.FrequencyPenalty),
		PresencePenalty:  float32(conf.Model.PresencePenalty),
	}
}

// sendAnswer converts the Markdown answer to Telegram HTML and sends it in
// chunks. If Telegram rejects the HTML, the chunk is resent as plain text.
func (b *Bot) sendAnswer(msg *tgbotapi.Message, answer string) {
	for i, chunk := range splitMessage(answer, chunkLimit) {
		m := tgbotapi.NewMessage(msg.Chat.ID, markdownToHTML(chunk))
		m.ParseMode = tgbotapi.ModeHTML
		m.DisableWebPagePreview = true
		if i == 0 {
			m.ReplyToMessageID = msg.MessageID
			m.AllowSendingWithoutReply = true
		}
		if _, err := b.tg.Send(m); err != nil {
			log.Printf("HTML send failed (%v), retrying as plain text", err)
			m.Text = chunk
			m.ParseMode = ""
			if _, err := b.tg.Send(m); err != nil {
				log.Printf("Plain send to %d failed: %v", msg.Chat.ID, err)
			}
		}
	}
}
