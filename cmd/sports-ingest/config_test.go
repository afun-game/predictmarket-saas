package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigUsesDefaults(t *testing.T) {
	config, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.LMBBaseURL != "https://lmb.com.mx" {
		t.Errorf("LMBBaseURL = %q, want https://lmb.com.mx", config.LMBBaseURL)
	}
	if got := config.LMBCalendarLocation.String(); got != "Asia/Shanghai" {
		t.Errorf("LMBCalendarLocation = %q, want Asia/Shanghai", got)
	}
	if got := config.LMBMarketLocation.String(); got != "America/Mexico_City" {
		t.Errorf("LMBMarketLocation = %q, want America/Mexico_City", got)
	}
	if config.LMBRequestTimeout != 15*time.Second {
		t.Errorf("LMBRequestTimeout = %s, want 15s", config.LMBRequestTimeout)
	}
	if config.PollInterval != 15*time.Minute {
		t.Errorf("PollInterval = %s, want 15m", config.PollInterval)
	}
	if config.LookaheadDays != 7 {
		t.Errorf("LookaheadDays = %d, want 7", config.LookaheadDays)
	}
	if config.RunOnce {
		t.Error("RunOnce = true, want false")
	}
}

func TestLoadConfigEnablesRunOnce(t *testing.T) {
	config, err := loadConfig(environmentValues(map[string]string{
		"SPORTS_INGEST_RUN_ONCE": "true",
	}))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if !config.RunOnce {
		t.Error("RunOnce = false, want true")
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name         string
		environment  map[string]string
		wantVariable string
	}{
		{
			name: "request timeout",
			environment: map[string]string{
				"LMB_REQUEST_TIMEOUT": "not-a-duration",
			},
			wantVariable: "LMB_REQUEST_TIMEOUT",
		},
		{
			name: "calendar timezone",
			environment: map[string]string{
				"LMB_CALENDAR_TIMEZONE": "Mars/Olympus",
			},
			wantVariable: "LMB_CALENDAR_TIMEZONE",
		},
		{
			name: "market timezone",
			environment: map[string]string{
				"LMB_MARKET_TIMEZONE": "Mars/Olympus",
			},
			wantVariable: "LMB_MARKET_TIMEZONE",
		},
		{
			name: "lookahead below range",
			environment: map[string]string{
				"SPORTS_INGEST_LOOKAHEAD_DAYS": "-1",
			},
			wantVariable: "SPORTS_INGEST_LOOKAHEAD_DAYS",
		},
		{
			name: "lookahead above range",
			environment: map[string]string{
				"SPORTS_INGEST_LOOKAHEAD_DAYS": "31",
			},
			wantVariable: "SPORTS_INGEST_LOOKAHEAD_DAYS",
		},
		{
			name: "base URL",
			environment: map[string]string{
				"LMB_BASE_URL": "ftp://lmb.example",
			},
			wantVariable: "LMB_BASE_URL",
		},
		{
			name: "run once",
			environment: map[string]string{
				"SPORTS_INGEST_RUN_ONCE": "sometimes",
			},
			wantVariable: "SPORTS_INGEST_RUN_ONCE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadConfig(environmentValues(test.environment))
			if err == nil {
				t.Fatalf("loadConfig() error = nil, want error naming %s", test.wantVariable)
			}
			if !strings.Contains(err.Error(), test.wantVariable) {
				t.Errorf("loadConfig() error = %q, want variable %q", err, test.wantVariable)
			}
		})
	}
}

func environmentValues(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
