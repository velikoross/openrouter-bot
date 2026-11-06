package bot

import "testing"

func TestStripMention(t *testing.T) {
	cases := []struct {
		in, out string
		ok      bool
	}{
		{"@Bot hi", "hi", true},
		{"@bot, hi", "hi", true},
		{"hi @BOT", "hi", true},
		{"hi @bot how are you", "hi how are you", true},
		{"@botty hi", "@botty hi", false},
		{"mail@bot hi", "mail@bot hi", false},
		{"no mention", "no mention", false},
	}
	for _, c := range cases {
		out, ok := stripMention(c.in, "Bot")
		if out != c.out || ok != c.ok {
			t.Errorf("stripMention(%q) = %q,%v want %q,%v", c.in, out, ok, c.out, c.ok)
		}
	}
	if _, ok := stripMention("@ hi", ""); ok {
		t.Error("empty username must never match")
	}
}

func TestMatchAlias(t *testing.T) {
	aliases := []string{"Пирдун", "gpt"}
	cases := []struct {
		in, out string
		ok      bool
	}{
		{"пирдун, привет", "привет", true},
		{"  Пирдун: что такое Go?", "что такое Go?", true},
		{"пирдун", "", true},
		{"слушай, пирдун, а ты кто?", "слушай, пирдун, а ты кто?", true},
		{"GPT — помоги", "помоги", true},
		{"пирдуну привет", "пирдуну привет", false},
		{"chatgpt лучше", "chatgpt лучше", false},
	}
	for _, c := range cases {
		out, ok := matchAlias(c.in, aliases)
		if out != c.out || ok != c.ok {
			t.Errorf("matchAlias(%q) = %q,%v want %q,%v", c.in, out, ok, c.out, c.ok)
		}
	}
	if _, ok := matchAlias("anything", nil); ok {
		t.Error("no aliases → no match")
	}
}
