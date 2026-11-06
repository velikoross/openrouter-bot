package bot

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML(t *testing.T) {
	cases := map[string]string{
		"plain <tag> & \"q\"":             "plain &lt;tag&gt; &amp; &quot;q&quot;",
		"**bold** and __bold__":           "<b>bold</b> and <b>bold</b>",
		"*italic* and _italic_":           "<i>italic</i> and <i>italic</i>",
		"~~gone~~":                        "<s>gone</s>",
		"snake_case_name stays":           "snake_case_name stays",
		"2 * 3 * 4":                       "2 * 3 * 4",
		"`a<b>` code":                     "<code>a&lt;b&gt;</code> code",
		"`**not bold**`":                  "<code>**not bold**</code>",
		"[site](https://ex.com/?a=1&b=2)": `<a href="https://ex.com/?a=1&amp;b=2">site</a>`,
		"[js](javascript:alert(1))":       "[js](javascript:alert(1))",
		"# Title":                         "<b>Title</b>",
		"### Sub **x** ###":               "<b>Sub <b>x</b></b>",
		"- item\n* item2\n  + nested":     "• item\n• item2\n  • nested",
		"---":                             "———",
		"> quoted\n> more\ntext":          "<blockquote>quoted\nmore</blockquote>\ntext",
		"```go\nif a < b {}\n```":         "<pre><code class=\"language-go\">if a &lt; b {}</code></pre>",
		"```\nno lang\n```":               "<pre><code>no lang</code></pre>",
		"```python\nunclosed":             "<pre><code class=\"language-python\">unclosed</code></pre>",
		"unclosed `code and **bold":       "unclosed `code and **bold",
	}
	for in, want := range cases {
		if got := markdownToHTML(in); got != want {
			t.Errorf("markdownToHTML(%q)\n got: %q\nwant: %q", in, got, want)
		}
	}
}

func TestMarkdownToHTMLCodeKeepsMarkup(t *testing.T) {
	in := "```\n**x** _y_ [a](https://b.c)\n```"
	if got := markdownToHTML(in); got != "<pre><code>**x** _y_ [a](https://b.c)</code></pre>" {
		t.Errorf("got %q", got)
	}
}

func TestCleanAnswer(t *testing.T) {
	if got := cleanAnswer("<think>\nreasoning\n</think>\n\nAnswer "); got != "Answer" {
		t.Errorf("got %q", got)
	}
	if got := cleanAnswer("a <think>x</think> b"); got != "a  b" {
		t.Errorf("got %q", got)
	}
}

func TestSplitMessageShort(t *testing.T) {
	if got := splitMessage("hello", 100); len(got) != 1 || got[0] != "hello" {
		t.Errorf("got %q", got)
	}
	if got := splitMessage("  \n ", 100); len(got) != 0 {
		t.Errorf("whitespace should give no chunks, got %q", got)
	}
}

func TestSplitMessagePrefersParagraphs(t *testing.T) {
	text := strings.Repeat("a", 60) + "\n\n" + strings.Repeat("b", 60)
	got := splitMessage(text, 100)
	if len(got) != 2 || got[0] != strings.Repeat("a", 60)+"\n\n" || got[1] != strings.Repeat("b", 60) {
		t.Errorf("got %q", got)
	}
}

func TestSplitMessageHardCutAndUnicode(t *testing.T) {
	text := strings.Repeat("я", 250) + strings.Repeat("😀", 50) // emoji = 2 UTF-16 units
	got := splitMessage(text, 100)
	if strings.Join(got, "") != text {
		t.Fatal("content lost")
	}
	for _, c := range got {
		if utf16Len(c) > 100 {
			t.Errorf("chunk too long: %d", utf16Len(c))
		}
		if !strings.ContainsRune(c, 'я') && !strings.ContainsRune(c, '😀') {
			t.Errorf("broken rune in %q", c)
		}
	}
}

func TestSplitMessageKeepsCodeFences(t *testing.T) {
	code := strings.Repeat("line of code\n", 30)
	text := "intro\n```go\n" + code + "```\noutro"
	got := splitMessage(text, 120)
	if len(got) < 3 {
		t.Fatalf("expected several chunks, got %d", len(got))
	}
	for i, c := range got {
		if utf16Len(c) > 120 {
			t.Errorf("chunk %d too long (%d)", i, utf16Len(c))
		}
		if n := strings.Count(c, "```"); n%2 != 0 {
			t.Errorf("chunk %d has unbalanced fences: %q", i, c)
		}
		if i > 0 && i < len(got)-1 && !strings.HasPrefix(c, "```go\n") {
			t.Errorf("chunk %d should reopen the code block: %q", i, c)
		}
		html := markdownToHTML(c)
		if strings.Count(html, "<pre>") != strings.Count(html, "</pre>") {
			t.Errorf("chunk %d: unbalanced html", i)
		}
	}
	joined := strings.Join(got, "")
	if strings.Count(joined, "line of code") != 30 {
		t.Error("lines lost")
	}
}

func TestTablesToLists(t *testing.T) {
	in := `Варианты:

| Решение | Docker/OCI | Maven | Raw/generic |
|---|:---:|---:|---|
| **JFrog Artifactory** | Да | Да | Да, Generic |
| Gitea | Да |  | Да |

Итого.`
	want := `Варианты:

**JFrog Artifactory**
- Docker/OCI: Да
- Maven: Да
- Raw/generic: Да, Generic

**Gitea**
- Docker/OCI: Да
- Raw/generic: Да

Итого.`
	if got := tablesToLists(in); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTwoColumnTable(t *testing.T) {
	in := "| Команда | Что делает |\n| --- | --- |\n| `ls` | список файлов |\n| du |  |"
	want := "- **`ls`** — список файлов\n- **du**"
	if got := tablesToLists(in); got != want {
		t.Errorf("got %q", got)
	}
	html := markdownToHTML(tablesToLists(in))
	if html != "• <b><code>ls</code></b> — список файлов\n• <b>du</b>" {
		t.Errorf("html %q", html)
	}
}

func TestTablesToListsKeepsOtherPipes(t *testing.T) {
	for _, in := range []string{
		"```\n| a | b |\n|---|---|\n| 1 | 2 |\n```", // inside code
		"| not a table |\nplain text",               // no separator
		"echo a | grep b",                           // shell pipe
	} {
		if got := tablesToLists(in); got != in {
			t.Errorf("%q changed to %q", in, got)
		}
	}
}

func TestTableAtEndOfText(t *testing.T) {
	in := "| a | b | c |\n|--|--|--|\n| x | 1 | 2 |"
	want := "**x**\n- b: 1\n- c: 2"
	if got := tablesToLists(in); got != want {
		t.Errorf("got %q", got)
	}
}
