package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"openrouter-bot/config"
	"openrouter-bot/lang"
	"openrouter-bot/user"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sashabaranov/go-openai"
)

type Model struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Pricing     struct {
		Prompt string `json:"prompt"`
	} `json:"pricing"`
}

type APIResponse struct {
	Data []Model `json:"data"`
}

// GetFreeModels теперь принимает конфиг напрямую — не создаёт новый Manager при каждом вызове
func GetFreeModels(conf *config.Config) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	resp, err := client.Get(conf.OpenAIBaseURL + "/models")
	if err != nil {
		return "", fmt.Errorf("error get models: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error read response: %v", err)
	}

	var apiResponse APIResponse
	if err = json.Unmarshal(body, &apiResponse); err != nil {
		return "", fmt.Errorf("error parse json: %v", err)
	}

	var result strings.Builder
	for _, model := range apiResponse.Data {
		if model.Pricing.Prompt == "0" {
			result.WriteString(fmt.Sprintf("➡ `%s`\n", model.ID))
		}
	}
	return result.String(), nil
}

// HandleChatGPTStreamResponse — принимает конфиг напрямую, добавляет reply и таймаут
func HandleChatGPTStreamResponse(
	bot *tgbotapi.BotAPI,
	client *openai.Client,
	message *tgbotapi.Message,
	conf *config.Config,
	userStats *user.UsageTracker,
) string {
	// Таймаут на весь запрос — предотвращает вечное зависание
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	userStats.CheckHistory(conf.MaxHistorySize, conf.MaxHistoryTime)
	userStats.LastMessageTime = time.Now()

	errorMessage := lang.Translate("errorText", conf.Lang)

	// Формируем историю сообщений
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: userStats.SystemPrompt,
		},
	}

	for _, msg := range userStats.GetMessages() {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	if conf.Vision == "true" {
		messages = append(messages, addVisionMessage(bot, message, conf))
	} else {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: message.Text,
		})
	}

	req := openai.ChatCompletionRequest{
		Model:            conf.Model.ModelName,
		FrequencyPenalty: float32(conf.Model.FrequencyPenalty),
		PresencePenalty:  float32(conf.Model.PresencePenalty),
		Temperature:      float32(conf.Model.Temperature),
		TopP:             float32(conf.Model.TopP),
		MaxTokens:        conf.MaxTokens,
		Messages:         messages,
		Stream:           false,
	}

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("ChatCompletion error: %v\n", err)
		errMsg := tgbotapi.NewMessage(message.Chat.ID, errorMessage)
		// Цитируем исходное сообщение даже при ошибке
		errMsg.ReplyToMessageID = message.MessageID
		bot.Send(errMsg)
		return ""
	}

	answer := resp.Choices[0].Message.Content

	// Записываем в историю
	userStats.AddMessage(openai.ChatMessageRoleUser, message.Text)
	userStats.AddMessage(openai.ChatMessageRoleAssistant, answer)

	// Отправляем ответ с цитированием исходного сообщения
	safe := tgbotapi.EscapeText(tgbotapi.ModeMarkdown, answer)
	msg := tgbotapi.NewMessage(message.Chat.ID, safe)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.ReplyToMessageID = message.MessageID // ← FIX: цитируем сообщение

	if _, err := bot.Send(msg); err != nil {
		// Fallback: если Markdown не парсится — отправляем plain text
		log.Printf("Markdown send error: %v, retrying as plain text", err)
		fallback := tgbotapi.NewMessage(message.Chat.ID, answer)
		fallback.ReplyToMessageID = message.MessageID
		bot.Send(fallback)
	}

	return resp.ID
}

func addVisionMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, conf *config.Config) openai.ChatCompletionMessage {
	if len(message.Photo) > 0 {
		photoSize := message.Photo[len(message.Photo)-1]
		fileID := photoSize.FileID

		file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
		if err != nil {
			log.Printf("Error getting file: %v", err)
			return openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: message.Text,
			}
		}

		fileURL := file.Link(bot.Token)
		fmt.Println("Photo URL:", fileURL)
		if message.Text == "" {
			message.Text = conf.VisionPrompt
		}

		return openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleUser,
			MultiContent: []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: message.Text,
				},
				{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    fileURL,
						Detail: openai.ImageURLDetail(conf.VisionDetails),
					},
				},
			},
		}
	}
	return openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: message.Text,
	}
}