package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestConfigureLoggingJSONAndLevel(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("APP_ENV", "test")
	var output bytes.Buffer
	if err := configureLogging(&output); err != nil {
		t.Fatalf("configureLogging() error = %v", err)
	}
	slog.Info("hidden")
	slog.Warn("visible", "event_id", "event-1")
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode JSON log %q: %v", output.String(), err)
	}
	messageMatches := entry["msg"] == "visible"
	serviceMatches := entry["service"] == "predictmarket-saas"
	environmentMatches := entry["environment"] == "test"
	if !messageMatches || !serviceMatches || !environmentMatches {
		t.Errorf("log entry = %#v", entry)
	}
	if entry["event_id"] != "event-1" {
		t.Errorf("event_id = %#v", entry["event_id"])
	}
}

func TestConfigureLoggingRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	if err := configureLogging(&bytes.Buffer{}); err == nil {
		t.Fatal("configureLogging() accepted invalid level")
	}
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "xml")
	if err := configureLogging(&bytes.Buffer{}); err == nil {
		t.Fatal("configureLogging() accepted invalid format")
	}
}
