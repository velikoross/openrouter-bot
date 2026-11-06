package bot

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// TelegramLimit is the max message length; we keep a margin for tags we add.
const TelegramLimit = 4096

const chunkLimit = 4000

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// cleanAnswer removes inline reasoning blocks some models emit.
func cleanAnswer(s string) string {
	return strings.TrimSpace(thinkRe.ReplaceAllString(s, ""))
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += len(utf16.Encode([]rune{r}))
	}
	return n
}

// prefixWithin returns the longest byte prefix of s that fits in limit
// UTF-16 units, cut on a rune boundary.
func prefixWithin(s string, limit int) int {
	n := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if n+w > limit {
			return i
		}
		n += w
	}
	return len(s)
}

// findCut picks a nice place to split s so that the first part fits in limit.
func findCut(s string, limit int) int {
	end := prefixWithin(s, limit)
	if end >= len(s) {
		return len(s)
	}
	head := s[:end]
	min := end / 2
	for _, sep := range []string{"\n\n", "\n", ". ", " "} {
		if i := strings.LastIndex(head, sep); i > min {
			return i + len(sep)
		}
	}
	if end == 0 { // limit smaller than one rune
		_, size := utf8.DecodeRuneInString(s)
		return size
	}
	return end
}

// fenceStateAfter tracks whether a ``` code block is open after text.
// It returns the opening fence line (e.g. "```go") or "" if closed.
func fenceStateAfter(open, text string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "```") {
			continue
		}
		if open == "" {
			open = t
		} else {
			open = ""
		}
	}
	return open
}

// splitMessage splits markdown text into chunks of at most limit UTF-16 units,
// preferring paragraph/line boundaries and keeping code fences balanced.
func splitMessage(text string, limit int) []string {
	const closeFence = "\n```"
	var chunks []string
	open := ""
	rest := text
	for strings.TrimSpace(rest) != "" {
		prefix := ""
		if open != "" {
			prefix = open + "\n"
		}
		if utf16Len(prefix+rest) <= limit {
			chunks = append(chunks, prefix+rest)
			break
		}
		budget := limit - utf16Len(prefix) - utf16Len(closeFence)
		if budget < 1 {
			budget = 1
		}
		cut := findCut(rest, budget)
		part := rest[:cut]
		rest = strings.TrimLeft(rest[cut:], "\n")
		nextOpen := fenceStateAfter(open, part)
		chunk := prefix + part
		if nextOpen != "" {
			chunk = strings.TrimRight(chunk, "\n") + closeFence
		}
		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}
		open = nextOpen
	}
	return chunks
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }

var (
	headingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)(?:\s+#+)?\s*$`)
	bulletRe  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	hrRe      = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	linkRe    = regexp.MustCompile(`\[([^\[\]\n]+)\]\((https?://[^\s()]+)\)`)
	boldRe    = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	bold2Re   = regexp.MustCompile(`__([^_\n]+?)__`)
	strikeRe  = regexp.MustCompile(`~~([^~\n]+?)~~`)
	italicRe  = regexp.MustCompile(`(^|[^*\w])\*([^*\s](?:[^*\n]*?[^*\s])?)\*([^*\w]|$)`)
	italic2Re = regexp.MustCompile(`(^|[^_\w])_([^_\s](?:[^_\n]*?[^_\s])?)_([^_\w]|$)`)
)

func renderText(s string) string {
	s = escapeHTML(s)
	s = linkRe.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = boldRe.ReplaceAllString(s, "<b>$1</b>")
	s = bold2Re.ReplaceAllString(s, "<b>$1</b>")
	s = strikeRe.ReplaceAllString(s, "<s>$1</s>")
	s = italicRe.ReplaceAllString(s, "$1<i>$2</i>$3")
	s = italic2Re.ReplaceAllString(s, "$1<i>$2</i>$3")
	return s
}

// renderInline handles `code` spans and inline formatting of one line.
func renderInline(line string) string {
	var b strings.Builder
	for {
		start := strings.Index(line, "`")
		if start < 0 {
			break
		}
		end := strings.Index(line[start+1:], "`")
		if end < 0 {
			break
		}
		b.WriteString(renderText(line[:start]))
		b.WriteString("<code>" + escapeHTML(line[start+1:start+1+end]) + "</code>")
		line = line[start+1+end+1:]
	}
	b.WriteString(renderText(line))
	return b.String()
}

func renderLine(line string) string {
	if hrRe.MatchString(line) {
		return "———"
	}
	if m := headingRe.FindStringSubmatch(line); m != nil {
		return "<b>" + renderInline(m[1]) + "</b>"
	}
	if m := bulletRe.FindStringSubmatch(line); m != nil {
		return m[1] + "• " + renderInline(m[2])
	}
	return renderInline(line)
}

// markdownToHTML converts the common Markdown produced by LLMs into the HTML
// subset supported by Telegram. Anything it does not understand is escaped
// and shown as text.
func markdownToHTML(md string) string {
	var out []string
	var code, quote []string
	inCode := false
	codeLang := ""

	flushQuote := func() {
		if len(quote) > 0 {
			out = append(out, "<blockquote>"+strings.Join(quote, "\n")+"</blockquote>")
			quote = nil
		}
	}
	flushCode := func() {
		open := "<pre><code>"
		if codeLang != "" {
			open = `<pre><code class="language-` + escapeHTML(codeLang) + `">`
		}
		out = append(out, open+escapeHTML(strings.Join(code, "\n"))+"</code></pre>")
		code = nil
	}

	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if inCode {
			if strings.HasPrefix(trimmed, "```") {
				flushCode()
				inCode = false
				continue
			}
			code = append(code, line)
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			flushQuote()
			inCode = true
			codeLang = ""
			if f := strings.Fields(strings.TrimPrefix(trimmed, "```")); len(f) > 0 {
				codeLang = f[0]
			}
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			quote = append(quote, renderLine(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))))
			continue
		}
		flushQuote()
		out = append(out, renderLine(line))
	}
	if inCode {
		flushCode()
	}
	flushQuote()
	return strings.Join(out, "\n")
}
