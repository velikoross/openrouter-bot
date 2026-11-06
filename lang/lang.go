// Package lang provides bot UI translations. Translation files are embedded
// into the binary, so the bot no longer depends on the working directory.
package lang

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed *.json
var files embed.FS

// Supported lists the languages shipped with the bot.
var Supported = []string{"EN", "RU"}

// Default is used when a requested language or key is missing.
const Default = "EN"

var (
	once         sync.Once
	loadErr      error
	translations map[string]map[string]interface{}
)

// LoadTranslations parses the embedded translation files. It is safe to call
// multiple times; Translate calls it lazily as well.
func LoadTranslations() error {
	once.Do(func() {
		translations = make(map[string]map[string]interface{}, len(Supported))
		for _, l := range Supported {
			data, err := files.ReadFile(l + ".json")
			if err != nil {
				loadErr = fmt.Errorf("read %s.json: %w", l, err)
				return
			}
			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				loadErr = fmt.Errorf("parse %s.json: %w", l, err)
				return
			}
			translations[l] = m
		}
	})
	return loadErr
}

// Normalize converts user input like "ru", " RU " into a supported language
// code. It returns "" for anything unsupported (e.g. a system locale such as
// "en_US.UTF-8").
func Normalize(l string) string {
	l = strings.ToUpper(strings.TrimSpace(l))
	for _, s := range Supported {
		if l == s {
			return s
		}
	}
	return ""
}

func lookup(key, l string) (string, bool) {
	var value interface{} = translations[l]
	for _, k := range strings.Split(key, ".") {
		m, ok := value.(map[string]interface{})
		if !ok {
			return "", false
		}
		value = m[k]
	}
	s, ok := value.(string)
	return s, ok
}

// Translate returns the string for key in language l, falling back to the
// default language and finally to the key itself.
func Translate(key string, l string) string {
	if err := LoadTranslations(); err != nil {
		return key
	}
	if n := Normalize(l); n != "" {
		if s, ok := lookup(key, n); ok {
			return s
		}
	}
	if s, ok := lookup(key, Default); ok {
		return s
	}
	return key
}
