package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

func escapeMarkdown(text string) string {
	text = strings.ReplaceAll(text, "_", "\\_")
	text = strings.ReplaceAll(text, "*", "\\*")
	text = strings.ReplaceAll(text, "[", "\\[")
	text = strings.ReplaceAll(text, "`", "\\`")
	return text
}

func escapeMarkdownPreservingEscapes(text string) string {
	var b strings.Builder
	prevBackslash := false
	for _, r := range text {
		if (r == '_' || r == '*' || r == '[' || r == '`') && !prevBackslash {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
		prevBackslash = r == '\\'
	}
	return b.String()
}

func telegramUsernameMentionMarkdown(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	return "@" + escapeMarkdown(username)
}

func inventoryItemMarkdownName(name string) string {
	name = strings.Map(func(r rune) rune {
		if containsDisallowedControl(string(r), false) {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "-"
	}
	return escapeMarkdown(name)
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

func getConfigIntFromDBChecked(db *gorm.DB, key string, defaultVal int) (int, error) {
	if db == nil {
		return defaultVal, nil
	}

	var cfg SystemConfig
	err := db.Where("key = ?", key).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && strings.TrimSpace(cfg.Value) == "") {
		return defaultVal, nil
	}
	if err != nil {
		return defaultVal, err
	}
	val, err := strconv.Atoi(strings.TrimSpace(cfg.Value))
	if err != nil {
		return defaultVal, fmt.Errorf("invalid integer config %s=%q: %w", key, cfg.Value, err)
	}
	return val, nil
}

func getConfigIntChecked(key string, defaultVal int) (int, error) {
	return getConfigIntFromDBChecked(DB, key, defaultVal)
}

func upsertSystemConfigValue(key string, value string) error {
	return upsertSystemConfigValueInTx(DB, key, value)
}
