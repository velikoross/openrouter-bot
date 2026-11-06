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

	resp, maxTokens, err := b.complete(ctx, req, func(id string) { b.recordCost(conf, usage, id) })
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

	answer, reason := answerOf(resp)
	if answer == "" {
		log.Printf("Empty answer from %s (finish_reason=%q, %s)", req.Model, reason, usageInfo(resp))
		text := b.t("emptyAnswer")
		if reason == openai.FinishReasonLength && conf.IsAdmin(from.ID) {
			text += "\n\n" + fmt.Sprintf(b.t("emptyAnswerLength"), maxTokens)
		}
		b.reply(msg, text)
		return
	}

	conv.AddExchange(userText, answer, conf.MaxHistorySize)
	if reason == openai.FinishReasonLength {
		answer += "\n\n" + b.t("truncatedAnswer")
	}
	b.sendAnswer(msg, answer)
}

func answerOf(resp openai.ChatCompletionResponse) (string, openai.FinishReason) {
	if len(resp.Choices) == 0 {
		return "", ""
	}
	return cleanAnswer(resp.Choices[0].Message.Content), resp.Choices[0].FinishReason
}

func usageInfo(resp openai.ChatCompletionResponse) string {
	u := resp.Usage
	reasoning := 0
	if u.CompletionTokensDetails != nil {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	return fmt.Sprintf("completion_tokens=%d reasoning_tokens=%d", u.CompletionTokens, reasoning)
}

// complete calls the LLM. Reasoning models spend max_tokens on thinking first
// and may run out before writing the answer (empty content, finish_reason
// "length"); in that case the request is retried once with a bigger limit.
// onGeneration is called for every generation id (each one costs money).
// It returns the response and the max_tokens used for it.
func (b *Bot) complete(ctx context.Context, req openai.ChatCompletionRequest, onGeneration func(id string)) (openai.ChatCompletionResponse, int, error) {
	resp, err := b.llm.CreateChatCompletion(ctx, req)
	if err != nil {
		return resp, req.MaxTokens, err
	}
	if resp.ID != "" {
		onGeneration(resp.ID)
	}
	answer, reason := answerOf(resp)
	if answer != "" || reason != openai.FinishReasonLength || req.MaxTokens <= 0 {
		return resp, req.MaxTokens, nil
	}
	retry := req
	retry.MaxTokens = req.MaxTokens * 4
	if retry.MaxTokens < 16000 {
		retry.MaxTokens = 16000
	}
	log.Printf("Empty answer from %s with max_tokens=%d (%s), retrying with %d",
		req.Model, req.MaxTokens, usageInfo(resp), retry.MaxTokens)
	resp2, err := b.llm.CreateChatCompletion(ctx, retry)
	if err != nil {
		if ctx.Err() != nil {
			return resp2, retry.MaxTokens, err // stopped or timed out
		}
		log.Printf("Retry failed: %v", err)
		return resp, req.MaxTokens, nil
	}
	if resp2.ID != "" {
		onGeneration(resp2.ID)
	}
	return resp2, retry.MaxTokens, nil
}

// recordCost asks OpenRouter for the generation cost in the background.
func (b *Bot) recordCost(conf *config.Config, usage *user.UsageTracker, id string) {
	if conf.Model.Type != "openrouter" || b.catalog == nil {
		return
	}
	b.goSafe(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cost, err := b.catalog.GenerationCost(ctx, id)
		if err != nil {
			log.Printf("Cost lookup for %s failed: %v", id, err)
			return
		}
		usage.AddCost(cost)
	})
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
	answer = tablesToLists(answer)
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
