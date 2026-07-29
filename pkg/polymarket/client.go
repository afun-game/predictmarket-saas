// Package polymarket provides a client for the public Polymarket Gamma API.
package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	GammaAPIBaseURL = "https://gamma-api.polymarket.com"
	CLOBAPIBaseURL  = "https://clob.polymarket.com"
	DataAPIBaseURL  = "https://data-api.polymarket.com"

	defaultTimeout           = 10 * time.Second
	defaultEventLimit        = 100
	maxEventLimit            = 500
	maxResponseBodyBytes     = 8 << 20
	maxErrorBodyBytes        = 4 << 10
	defaultMaxRetries        = 2
	defaultRetryInitialDelay = 100 * time.Millisecond
)

// Client is a Polymarket Gamma API client.
type Client struct {
	httpClient        *http.Client
	baseURL           string
	maxRetries        int
	retryInitialDelay time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the Gamma API URL. It is primarily useful for tests.
func WithBaseURL(baseURL string) Option {
	return func(client *Client) {
		client.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

// WithRetry configures retries after transport failures, rate limits, and server errors.
func WithRetry(maxRetries int, initialDelay time.Duration) Option {
	return func(client *Client) {
		if maxRetries >= 0 {
			client.maxRetries = maxRetries
		}
		if initialDelay >= 0 {
			client.retryInitialDelay = initialDelay
		}
	}
}

// NewClient creates a Polymarket client.
func NewClient(options ...Option) *Client {
	client := &Client{
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		baseURL:           GammaAPIBaseURL,
		maxRetries:        defaultMaxRetries,
		retryInitialDelay: defaultRetryInitialDelay,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// ListEventsOptions selects a page of events. TagSlug corresponds to the
// Gamma API's tag_slug category filter.
type ListEventsOptions struct {
	TagSlug   string
	SeriesID  string
	Active    *bool
	Closed    *bool
	Order     string
	Ascending *bool
	Limit     int
	Offset    int
}

// Event represents the subset of a Polymarket event used by this service.
type Event struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	EndDate     time.Time `json:"endDate"`
	StartTime   time.Time `json:"startTime"`
	GameID      int64     `json:"gameId"`
	SeriesSlug  string    `json:"seriesSlug"`
	Active      bool      `json:"active"`
	Closed      bool      `json:"closed"`
	Markets     []Market  `json:"markets"`
	Tags        []Tag     `json:"tags"`
	Series      []Series  `json:"series"`
	Teams       []Team    `json:"teams"`
	Sport       *Sport    `json:"sport"`
}

// Sport describes a league available through Gamma's sports catalog.
type Sport struct {
	ID         int64  `json:"id"`
	Sport      string `json:"sport"`
	Ordering   string `json:"ordering"`
	Tags       string `json:"tags"`
	SeriesID   string `json:"series"`
	Resolution string `json:"resolution"`
}

// Series identifies the league series attached to a sports event.
type Series struct {
	ID     string `json:"id"`
	Ticker string `json:"ticker"`
	Slug   string `json:"slug"`
	Title  string `json:"title"`
}

// Team contains structured team metadata returned for game events.
type Team struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	League       string `json:"league"`
	Abbreviation string `json:"abbreviation"`
	Alias        string `json:"alias"`
	Ordering     string `json:"ordering"`
}

// Tag describes a Polymarket event category.
type Tag struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Slug  string `json:"slug"`
}

// Market represents the subset of a Polymarket market used by this service.
type Market struct {
	ID               string    `json:"id"`
	Question         string    `json:"question"`
	EndDate          time.Time `json:"endDate"`
	Outcomes         []string  `json:"outcomes"`
	OutcomePrices    []float64 `json:"outcomePrices"`
	Volume           float64   `json:"volume"`
	Active           bool      `json:"active"`
	Closed           bool      `json:"closed"`
	GroupItemTitle   string    `json:"groupItemTitle"`
	SportsMarketType string    `json:"sportsMarketType"`
}

// UnmarshalJSON handles Gamma market fields that are encoded as JSON strings.
func (market *Market) UnmarshalJSON(data []byte) error {
	type marketAlias Market
	var payload struct {
		marketAlias
		Outcomes      json.RawMessage `json:"outcomes"`
		OutcomePrices json.RawMessage `json:"outcomePrices"`
		Volume        json.RawMessage `json:"volume"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("decode market: %w", err)
	}

	outcomes, err := decodeStringSlice(payload.Outcomes)
	if err != nil {
		return fmt.Errorf("decode market outcomes: %w", err)
	}
	prices, err := decodeFloatSlice(payload.OutcomePrices)
	if err != nil {
		return fmt.Errorf("decode market outcome prices: %w", err)
	}
	volume, err := decodeFloat(payload.Volume)
	if err != nil {
		return fmt.Errorf("decode market volume: %w", err)
	}

	*market = Market(payload.marketAlias)
	market.Outcomes = outcomes
	market.OutcomePrices = prices
	market.Volume = volume
	return nil
}

// HTTPError reports a non-success response from Polymarket.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("Polymarket API returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("Polymarket API returned status %d: %s", e.StatusCode, e.Body)
}

// GetEvents retrieves up to 100 events. Category is treated as a tag slug.
// New code that needs pagination should use ListEvents.
func (c *Client) GetEvents(ctx context.Context, category string, active bool) ([]Event, error) {
	closed := !active
	return c.ListEvents(ctx, ListEventsOptions{
		TagSlug: category,
		Active:  &active,
		Closed:  &closed,
		Limit:   defaultEventLimit,
	})
}

// ListEvents retrieves a filtered page of events from Polymarket.
func (c *Client) ListEvents(ctx context.Context, options ListEventsOptions) ([]Event, error) {
	if options.Limit == 0 {
		options.Limit = defaultEventLimit
	}
	if options.Limit < 1 || options.Limit > maxEventLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d", maxEventLimit)
	}
	if options.Offset < 0 {
		return nil, errors.New("offset must not be negative")
	}

	requestURL, err := c.endpoint("events")
	if err != nil {
		return nil, err
	}
	query := requestURL.Query()
	query.Set("limit", strconv.Itoa(options.Limit))
	query.Set("offset", strconv.Itoa(options.Offset))
	if tagSlug := strings.TrimSpace(options.TagSlug); tagSlug != "" {
		query.Set("tag_slug", tagSlug)
	}
	if seriesID := strings.TrimSpace(options.SeriesID); seriesID != "" {
		query.Set("series_id", seriesID)
	}
	if options.Active != nil {
		query.Set("active", strconv.FormatBool(*options.Active))
	}
	if options.Closed != nil {
		query.Set("closed", strconv.FormatBool(*options.Closed))
	}
	if order := strings.TrimSpace(options.Order); order != "" {
		query.Set("order", order)
	}
	if options.Ascending != nil {
		query.Set("ascending", strconv.FormatBool(*options.Ascending))
	}
	requestURL.RawQuery = query.Encode()

	events := []Event{}
	if err := c.getJSON(ctx, requestURL, &events); err != nil {
		return nil, fmt.Errorf("list Polymarket events: %w", err)
	}
	for i := range events {
		normalizeEvent(&events[i])
	}
	return events, nil
}

// ListSports retrieves the Gamma sports and league catalog.
func (c *Client) ListSports(ctx context.Context) ([]Sport, error) {
	requestURL, err := c.endpoint("sports")
	if err != nil {
		return nil, err
	}
	sports := []Sport{}
	if err := c.getJSON(ctx, requestURL, &sports); err != nil {
		return nil, fmt.Errorf("list Polymarket sports: %w", err)
	}
	return sports, nil
}

// GetEvent retrieves a single event by its numeric Gamma API ID.
func (c *Client) GetEvent(ctx context.Context, eventID string) (*Event, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, errors.New("event ID is required")
	}
	requestURL, err := c.endpoint("events", eventID)
	if err != nil {
		return nil, err
	}

	event := &Event{}
	if err := c.getJSON(ctx, requestURL, event); err != nil {
		return nil, fmt.Errorf("get Polymarket event %q: %w", eventID, err)
	}
	normalizeEvent(event)
	return event, nil
}

func (c *Client) endpoint(pathElements ...string) (*url.URL, error) {
	baseURL, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Polymarket base URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("Polymarket base URL must use http or https: %q", c.baseURL)
	}
	if baseURL.Host == "" {
		return nil, fmt.Errorf("Polymarket base URL has no host: %q", c.baseURL)
	}
	return baseURL.JoinPath(pathElements...), nil
}

func (c *Client) getJSON(ctx context.Context, requestURL *url.URL, destination any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := waitForRetry(ctx, c.retryDelay(attempt)); err != nil {
				return err
			}
		}

		retry, err := c.getJSONOnce(ctx, requestURL, destination)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
	}
	return lastErr
}

func (c *Client) getJSONOnce(
	ctx context.Context,
	requestURL *url.URL,
	destination any,
) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "predictmarket-saas/0.1")

	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return true, fmt.Errorf("send request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return false, fmt.Errorf("read response: %w", readErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close response: %w", closeErr)
	}
	if len(body) > maxResponseBodyBytes {
		return false, fmt.Errorf("response exceeds %d bytes", maxResponseBodyBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		httpErr := &HTTPError{
			StatusCode: response.StatusCode,
			Body:       errorBody(body),
		}
		return retryableStatus(response.StatusCode), httpErr
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	return false, nil
}

func (c *Client) retryDelay(attempt int) time.Duration {
	delay := c.retryInitialDelay
	for range attempt - 1 {
		delay *= 2
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func normalizeEvent(event *Event) {
	if event.Markets == nil {
		event.Markets = []Market{}
	}
	if event.Tags == nil {
		event.Tags = []Tag{}
	}
	if event.Series == nil {
		event.Series = []Series{}
	}
	if event.Teams == nil {
		event.Teams = []Team{}
	}
	for i := range event.Markets {
		if event.Markets[i].Outcomes == nil {
			event.Markets[i].Outcomes = []string{}
		}
		if event.Markets[i].OutcomePrices == nil {
			event.Markets[i].OutcomePrices = []float64{}
		}
	}
}

func errorBody(body []byte) string {
	if len(body) <= maxErrorBodyBytes {
		return strings.TrimSpace(string(body))
	}
	return strings.TrimSpace(string(body[:maxErrorBodyBytes])) + "..."
}

func decodeStringSlice(raw json.RawMessage) ([]string, error) {
	result := []string{}
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return result, nil
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func decodeFloatSlice(raw json.RawMessage) ([]float64, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return []float64{}, nil
	}
	var encodedValues []json.RawMessage
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &encodedValues); err != nil {
			return nil, err
		}
	} else if err := json.Unmarshal(raw, &encodedValues); err != nil {
		return nil, err
	}

	result := make([]float64, 0, len(encodedValues))
	for _, encodedValue := range encodedValues {
		value, err := decodeFloat(encodedValue)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func decodeFloat(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return 0, nil
	}
	if raw[0] != '"' {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		return value, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(encoded, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q as a number: %w", encoded, err)
	}
	return value, nil
}
