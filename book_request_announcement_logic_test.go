package main

import (
	"sync"
	"testing"
	"time"
)

func TestBookAnnouncementPreviewCandidateTokenResolvesUnderscoreItemID(t *testing.T) {
	bookRequestAnnouncementPreviewItems = sync.Map{}

	itemID := "lib_item_with_under_scores"
	token := storeBookAnnouncementPreviewCandidate(42, itemID)
	if len("br_ann_pub_42_"+token) > 64 {
		t.Fatalf("callback data is too long: %d", len("br_ann_pub_42_"+token))
	}

	reqID, parsedToken, ok := parseBookAnnouncementPublishCallback("br_ann_pub_42_" + token)
	if !ok {
		t.Fatal("publish callback should parse")
	}
	if reqID != 42 || parsedToken != token {
		t.Fatalf("parsed callback = %d/%q, want 42/%q", reqID, parsedToken, token)
	}

	got, ok := resolveBookAnnouncementPreviewCandidate(reqID, parsedToken, time.Now())
	if !ok || got != itemID {
		t.Fatalf("resolved item = %q/%v, want %q/true", got, ok, itemID)
	}
}

func TestBookAnnouncementPreviewCandidateExpires(t *testing.T) {
	bookRequestAnnouncementPreviewItems = sync.Map{}

	bookRequestAnnouncementPreviewItems.Store("expired", bookAnnouncementPreviewEntry{
		ReqID:     7,
		ItemID:    "book-7",
		ExpiresAt: time.Now().Add(-time.Second),
	})

	if got, ok := resolveBookAnnouncementPreviewCandidate(7, "expired", time.Now()); ok || got != "" {
		t.Fatalf("expired candidate resolved = %q/%v, want empty/false", got, ok)
	}
	if _, ok := bookRequestAnnouncementPreviewItems.Load("expired"); ok {
		t.Fatal("expired preview candidate should be removed")
	}
}
