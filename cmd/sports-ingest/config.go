package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDatabaseURL         = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"
	defaultLMBBaseURL          = "https://lmb.com.mx"
	defaultLMBCalendarTimezone = "Asia/Shanghai"
	defaultLMBMarketTimezone   = "America/Mexico_City"
	defaultLMBRequestTimeout   = 15 * time.Second
	defaultSportsPollInterval  = 5 * time.Minute
	defaultSportsLookaheadDays = 7
	maximumSportsLookaheadDays = 30
)

// config contains the standalone worker's runtime settings.
//
// LMBCalendarLocation controls the date and day-bounds sent to LMB. It is
// intentionally independent from LMBMarketLocation, which is the business and
// display timezone for LMB prediction markets. All stored event timestamps are
// UTC instants.
type config struct {
	DatabaseURL         string
	LMBBaseURL          string
	LMBCalendarLocation *time.Location
	LMBMarketLocation   *time.Location
	LMBRequestTimeout   time.Duration
	PollInterval        time.Duration
	LookaheadDays       int
	RunOnce             bool
}

func loadConfig(getenv func(string) string) (config, error) {
	if getenv == nil {
		return config{}, fmt.Errorf("environment lookup is required")
	}

	value := config{
		DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL")),
		LMBBaseURL:  configuredString(getenv, "LMB_BASE_URL", defaultLMBBaseURL),
	}
	if value.DatabaseURL == "" {
		value.DatabaseURL = defaultDatabaseURL
	}

	if err := validateHTTPURL(value.LMBBaseURL); err != nil {
		return config{}, fmt.Errorf("LMB_BASE_URL: %w", err)
	}

	var err error
	value.LMBCalendarLocation, err = loadNamedLocation(
		"LMB_CALENDAR_TIMEZONE",
		configuredString(getenv, "LMB_CALENDAR_TIMEZONE", defaultLMBCalendarTimezone),
	)
	if err != nil {
		return config{}, err
	}
	value.LMBMarketLocation, err = loadNamedLocation(
		"LMB_MARKET_TIMEZONE",
		configuredString(getenv, "LMB_MARKET_TIMEZONE", defaultLMBMarketTimezone),
	)
	if err != nil {
		return config{}, err
	}

	value.LMBRequestTimeout, err = loadPositiveDuration(
		"LMB_REQUEST_TIMEOUT",
		configuredString(getenv, "LMB_REQUEST_TIMEOUT", defaultLMBRequestTimeout.String()),
	)
	if err != nil {
		return config{}, err
	}
	value.PollInterval, err = loadPositiveDuration(
		"SPORTS_INGEST_POLL_INTERVAL",
		configuredString(getenv, "SPORTS_INGEST_POLL_INTERVAL", defaultSportsPollInterval.String()),
	)
	if err != nil {
		return config{}, err
	}

	value.LookaheadDays, err = loadLookaheadDays(
		configuredString(getenv, "SPORTS_INGEST_LOOKAHEAD_DAYS", strconv.Itoa(defaultSportsLookaheadDays)),
	)
	if err != nil {
		return config{}, err
	}
	value.RunOnce, err = loadBoolean(
		"SPORTS_INGEST_RUN_ONCE",
		configuredString(getenv, "SPORTS_INGEST_RUN_ONCE", "false"),
	)
	if err != nil {
		return config{}, err
	}
	return value, nil
}

func configuredString(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}

func validateHTTPURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return fmt.Errorf("must be an absolute HTTP(S) URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func loadNamedLocation(variable, value string) (*time.Location, error) {
	if !strings.Contains(value, "/") {
		return nil, fmt.Errorf("%s must be a named IANA timezone", variable)
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be a named IANA timezone: %w", variable, err)
	}
	return location, nil
}

func loadPositiveDuration(variable, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", variable, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", variable)
	}
	return duration, nil
}

func loadLookaheadDays(value string) (int, error) {
	lookaheadDays, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("SPORTS_INGEST_LOOKAHEAD_DAYS must be an integer: %w", err)
	}
	if lookaheadDays < 0 || lookaheadDays > maximumSportsLookaheadDays {
		return 0, fmt.Errorf(
			"SPORTS_INGEST_LOOKAHEAD_DAYS must be between 0 and %d",
			maximumSportsLookaheadDays,
		)
	}
	return lookaheadDays, nil
}

func loadBoolean(variable, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", variable, err)
	}
	return parsed, nil
}
