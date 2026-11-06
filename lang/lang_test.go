package lang

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	if got := Translate("commands.stop", "RU"); got != "Запрос остановлен." {
		t.Errorf("RU: %q", got)
	}
	if got := Translate("commands.stop", "ru"); got != "Запрос остановлен." {
		t.Errorf("lower-case code: %q", got)
	}
	if got := Translate("commands.stop", "en_US.UTF-8"); got != "Request stopped." {
		t.Errorf("unknown language should fall back to EN: %q", got)
	}
	if got := Translate("no.such.key", "RU"); got != "no.such.key" {
		t.Errorf("missing key: %q", got)
	}
	if got := Translate("commands", "RU"); got != "commands" {
		t.Errorf("non-string value: %q", got)
	}
}

func keys(prefix string, m map[string]interface{}, out *[]string) {
	for k, v := range m {
		if sub, ok := v.(map[string]interface{}); ok {
			keys(prefix+k+".", sub, out)
		} else {
			*out = append(*out, prefix+k)
		}
	}
}

// All languages must have the same keys and the same number of %s.
func TestLanguagesAreInSync(t *testing.T) {
	all := map[string][]string{}
	for _, l := range Supported {
		raw, _ := files.ReadFile(l + ".json")
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		var k []string
		keys("", m, &k)
		sort.Strings(k)
		all[l] = k
	}
	en := strings.Join(all["EN"], ",")
	for l, k := range all {
		if strings.Join(k, ",") != en {
			t.Errorf("%s keys differ from EN", l)
		}
	}
	for _, k := range all["EN"] {
		e, r := Translate(k, "EN"), Translate(k, "RU")
		if strings.Count(e, "%s") != strings.Count(r, "%s") {
			t.Errorf("%s: placeholders differ", k)
		}
	}
}
