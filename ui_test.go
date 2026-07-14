package main

import (
	"encoding/json"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestUserMainMenuReplyMarkupUsesTextGardenButton(t *testing.T) {
	previous := AppConfig
	AppConfig = &Config{
		GardenMiniAppEnabled: true,
		GardenMiniAppURL:     "https://yaoyuan.example.com/garden",
	}
	t.Cleanup(func() {
		AppConfig = previous
	})

	data, err := json.Marshal(userMainMenuReplyMarkup())
	if err != nil {
		t.Fatalf("marshal reply markup: %v", err)
	}
	raw := string(data)

	if !strings.Contains(raw, `"text":"`+userMenuGardenText+`"`) {
		t.Fatalf("garden text button missing: %s", raw)
	}
	if strings.Contains(raw, `"web_app"`) {
		t.Fatalf("reply keyboard garden entry should be text-only: %s", raw)
	}
	if strings.Contains(raw, userMenuGardenMiniAppText) {
		t.Fatalf("reply keyboard should not use direct mini app text: %s", raw)
	}
}

func TestGardenInlineMarkupWithMiniAppAppendsWebAppButton(t *testing.T) {
	previous := AppConfig
	AppConfig = &Config{
		GardenMiniAppEnabled: true,
		GardenMiniAppURL:     "https://yaoyuan.example.com/garden",
	}
	t.Cleanup(func() {
		AppConfig = previous
	})

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("灵田管理", "garden:fields"),
		),
	)
	data, err := json.Marshal(gardenInlineMarkupWithMiniApp(markup))
	if err != nil {
		t.Fatalf("marshal garden markup: %v", err)
	}
	raw := string(data)

	if !strings.Contains(raw, `"callback_data":"garden:fields"`) {
		t.Fatalf("original text interaction button missing: %s", raw)
	}
	if !strings.Contains(raw, `"text":"打开药园"`) || !strings.Contains(raw, `"web_app":{"url":"https://yaoyuan.example.com/garden"}`) {
		t.Fatalf("mini app web_app button missing: %s", raw)
	}
}

func TestGardenInlineMarkupWithMiniAppDisabledKeepsPlainMarkup(t *testing.T) {
	previous := AppConfig
	AppConfig = &Config{}
	t.Cleanup(func() {
		AppConfig = previous
	})

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("灵田管理", "garden:fields"),
		),
	)
	data, err := json.Marshal(gardenInlineMarkupWithMiniApp(markup))
	if err != nil {
		t.Fatalf("marshal garden markup: %v", err)
	}
	raw := string(data)

	if !strings.Contains(raw, `"callback_data":"garden:fields"`) {
		t.Fatalf("original text interaction button missing: %s", raw)
	}
	if strings.Contains(raw, `"web_app"`) || strings.Contains(raw, `"打开药园"`) {
		t.Fatalf("mini app button should be absent when disabled: %s", raw)
	}
}
