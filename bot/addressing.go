package bot

import (
	"strings"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// addressedText decides whether a non-command message is meant for the bot
// and returns the prompt with the mention/alias removed.
//
// Private chats: every message. Groups: replies to the bot's messages,
// @username mentions and configured short names (BOT_ALIASES).
func (b *Bot) addressedText(msg *tgbotapi.Message) (string, bool) {
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if msg.Chat.IsPrivate() {
		return strings.TrimSpace(text), true
	}

	explicit := false
	if t, ok := stripMention(text, b.self.UserName); ok {
		text, explicit = t, true
	}
	if t, ok := matchAlias(text, b.conf().BotAliases); ok {
		text, explicit = t, true
	}
	text = strings.TrimSpace(text)
	if explicit {
		return text, true
	}
	if r := msg.ReplyToMessage; r != nil && r.From != nil && r.From.ID == b.self.ID {
		// A sticker or other non-text reply to the bot is not a question.
		return text, text != "" || len(msg.Photo) > 0
	}
	return "", false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// findWord finds needle (already lower-cased runes) in hay as a whole word,
// case-insensitively. Returns the rune index or -1.
func findWord(hay []rune, needle []rune) int {
	if len(needle) == 0 {
		return -1
	}
	lower := make([]rune, len(hay))
	for i, r := range hay {
		lower[i] = unicode.ToLower(r)
	}
outer:
	for i := 0; i+len(needle) <= len(lower); i++ {
		for j, r := range needle {
			if lower[i+j] != r {
				continue outer
			}
		}
		if i > 0 && isWordRune(lower[i-1]) {
			continue
		}
		if end := i + len(needle); end < len(lower) && isWordRune(lower[end]) {
			continue
		}
		return i
	}
	return -1
}

func lowerRunes(s string) []rune {
	r := []rune(strings.TrimSpace(s))
	for i := range r {
		r[i] = unicode.ToLower(r[i])
	}
	return r
}

// trimAddress removes punctuation left after cutting a name: "бот, привет".
func trimAddress(s string) string {
	return strings.TrimLeftFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",.:;!?-—–", r)
	})
}

// stripMention removes "@username" from text.
func stripMention(text, username string) (string, bool) {
	if username == "" {
		return text, false
	}
	hay := []rune(text)
	needle := lowerRunes("@" + username)
	i := findWord(hay, needle)
	if i < 0 {
		return text, false
	}
	before := strings.TrimRightFunc(string(hay[:i]), unicode.IsSpace)
	after := trimAddress(string(hay[i+len(needle):]))
	if before == "" {
		return after, true
	}
	if after == "" {
		return before, true
	}
	return before + " " + after, true
}

// matchAlias reports whether one of the aliases is used as a word. When the
// message starts with the alias ("Пирдун, как дела?") it is removed.
func matchAlias(text string, aliases []string) (string, bool) {
	hay := []rune(text)
	lead := len(hay) - len([]rune(strings.TrimLeftFunc(text, unicode.IsSpace)))
	for _, a := range aliases {
		needle := lowerRunes(a)
		i := findWord(hay, needle)
		if i < 0 {
			continue
		}
		if i == lead {
			return trimAddress(string(hay[i+len(needle):])), true
		}
		return text, true
	}
	return text, false
}
