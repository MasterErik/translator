package common

import (
	"errors"
	"testing"
	"time"
)

func TestSTTEventCreation(t *testing.T) {
	ts := time.Now()
	evt := STTEvent{
		Text:      "Hello, world",
		Event:     EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: ts,
		Error:     nil,
	}

	if evt.Text != "Hello, world" {
		t.Errorf("Text: got %q, want %q", evt.Text, "Hello, world")
	}
	if evt.Event != EventEndOfTurn {
		t.Errorf("Event: got %q, want %q", evt.Event, EventEndOfTurn)
	}
	if evt.ChannelID != "speaker" {
		t.Errorf("ChannelID: got %q, want %q", evt.ChannelID, "speaker")
	}
	if !evt.Timestamp.Equal(ts) {
		t.Errorf("Timestamp: got %v, want %v", evt.Timestamp, ts)
	}
	if evt.Error != nil {
		t.Error("Error should be nil")
	}
}

func TestSTTEventWithError(t *testing.T) {
	evt := STTEvent{
		Text:      "",
		Event:     EventEndOfTurn,
		ChannelID: "mic",
		Timestamp: time.Now(),
		Error:     errors.New("connection lost"),
	}

	if evt.Error == nil {
		t.Fatal("expected an error")
	}
	if evt.Error.Error() != "connection lost" {
		t.Errorf("Error: got %q, want %q", evt.Error.Error(), "connection lost")
	}
	if evt.ChannelID != "mic" {
		t.Errorf("ChannelID: got %q, want %q", evt.ChannelID, "mic")
	}
}

func TestSTTEventInterim(t *testing.T) {
	evt := STTEvent{
		Text:      "Hel",
		Event:     EventUpdate,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	if evt.Event != EventUpdate {
		t.Error("interim event should have Event=EventUpdate")
	}
}

func TestUIEventTranslation(t *testing.T) {
	ts := time.Now()
	evt := UIEvent{
		Type:      UIEventTranslation,
		Text:      "Привет, мир",
		Timestamp: ts,
	}

	if evt.Type != UIEventTranslation {
		t.Errorf("Type: got %q, want %q", evt.Type, UIEventTranslation)
	}
	if evt.Text != "Привет, мир" {
		t.Errorf("Text: got %q, want %q", evt.Text, "Привет, мир")
	}
	if len(evt.Answers) != 0 {
		t.Errorf("Answers should be empty for Translation event, got %v", evt.Answers)
	}
}

func TestUIEventAnswerCandidates(t *testing.T) {
	evt := UIEvent{
		Type:      UIEventAnswerCandidates,
		Text:      "What is a goroutine?",
		Answers:   []string{"A lightweight thread", "A concurrent function"},
		Timestamp: time.Now(),
	}

	if evt.Type != UIEventAnswerCandidates {
		t.Errorf("Type: got %q, want %q", evt.Type, UIEventAnswerCandidates)
	}
	if len(evt.Answers) != 2 {
		t.Errorf("Answers length: got %d, want %d", len(evt.Answers), 2)
	}
	if evt.Answers[0] != "A lightweight thread" {
		t.Errorf("Answers[0]: got %q, want %q", evt.Answers[0], "A lightweight thread")
	}
}

func TestUIEventAnswer(t *testing.T) {
	evt := UIEvent{
		Type:      UIEventAnswer,
		Text:      "What is an interface?",
		Timestamp: time.Now(),
	}

	if evt.Type != UIEventAnswer {
		t.Errorf("Type: got %q, want %q", evt.Type, UIEventAnswer)
	}
}
