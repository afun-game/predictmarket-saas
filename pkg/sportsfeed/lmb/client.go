// Package lmb provides a client for Liga Mexicana de Beisbol's public calendar API.
package lmb

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
	// DefaultBaseURL is the public LMB website URL.
	DefaultBaseURL = "https://lmb.com.mx"

	defaultTimeout       = 10 * time.Second
	maxResponseBodyBytes = int64(4 << 20)
	maxErrorBodyBytes    = int64(4 << 10)
)

var (
	// ErrResponseTooLarge indicates that an LMB response exceeds the supported size.
	ErrResponseTooLarge = errors.New("LMB calendar response exceeds 4 MiB")
	// ErrMissingGamesInfo indicates that the LMB response did not include a schedule.
	ErrMissingGamesInfo = errors.New("LMB calendar response is missing games_info")
)

var defaultCalendarLocation = loadDefaultCalendarLocation()

// Client retrieves data from LMB's public calendar API.
type Client struct {
	httpClient       *http.Client
	baseURL          string
	calendarLocation *time.Location
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the LMB API base URL. It is primarily useful for tests.
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

// WithCalendarLocation sets the location used to construct LMB calendar-day
// query bounds. A nil location leaves the default Asia/Shanghai location intact.
func WithCalendarLocation(location *time.Location) Option {
	return func(client *Client) {
		if location != nil {
			client.calendarLocation = location
		}
	}
}

// NewClient creates an LMB calendar client.
func NewClient(options ...Option) *Client {
	client := &Client{
		httpClient:       &http.Client{Timeout: defaultTimeout},
		baseURL:          DefaultBaseURL,
		calendarLocation: defaultCalendarLocation,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// Team is a team returned by the LMB calendar API.
type Team struct {
	TeamUUID  string `json:"teamUuid"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
}

// Game is the subset of an LMB calendar game used by the sports feed.
type Game struct {
	GameID         int64  `json:"gameId"`
	Status         string `json:"status"`
	DetailedStatus string `json:"detailedStatus"`
	CanceledStatus string `json:"canceledSubStatus"`
	DateTime       int64  `json:"date_time"`
	AwayTeam       Team   `json:"awayTeam"`
	LocalTeam      Team   `json:"localTeam"`
}

// HTTPError reports a non-success response from the LMB API.
type HTTPError struct {
	StatusCode int
	Body       string
	ReadError  error
}

func (e *HTTPError) Error() string {
	if e.ReadError != nil {
		if e.Body == "" {
			return fmt.Sprintf("LMB calendar API returned status %d while reading error body: %v", e.StatusCode, e.ReadError)
		}
		return fmt.Sprintf("LMB calendar API returned status %d: %s (read error: %v)", e.StatusCode, e.Body, e.ReadError)
	}
	if e.Body == "" {
		return fmt.Sprintf("LMB calendar API returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("LMB calendar API returned status %d: %s", e.StatusCode, e.Body)
}

// Unwrap exposes an error encountered while reading the error response body.
func (e *HTTPError) Unwrap() error {
	return e.ReadError
}

// Calendar retrieves games for day. day is a calendar-date value: its year,
// month, and day are used unchanged to construct midnight in the configured
// calendar location, without first converting day into that location.
func (c *Client) Calendar(ctx context.Context, day time.Time) ([]Game, error) {
	requestURL, err := c.calendarURL(day)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create LMB calendar request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request LMB calendar: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := readErrorBody(response.Body)
		return nil, &HTTPError{
			StatusCode: response.StatusCode,
			Body:       body,
			ReadError:  readErr,
		}
	}
	if response.ContentLength > maxResponseBodyBytes {
		return nil, fmt.Errorf("%w: Content-Length %d", ErrResponseTooLarge, response.ContentLength)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read LMB calendar: %w", err)
	}
	if len(body) > int(maxResponseBodyBytes) {
		return nil, ErrResponseTooLarge
	}

	var payload calendarResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode LMB calendar: %w", err)
	}
	if payload.GamesInfo == nil {
		return nil, ErrMissingGamesInfo
	}
	return *payload.GamesInfo, nil
}

type calendarResponse struct {
	GamesInfo *[]Game `json:"games_info"`
}

func (c *Client) calendarURL(day time.Time) (*url.URL, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse LMB base URL: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("parse LMB base URL: URL scheme must be http or https")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("parse LMB base URL: URL must include a host")
	}

	year, month, dayOfMonth := day.Date()
	start := time.Date(year, month, dayOfMonth, 0, 0, 0, 0, c.calendarLocation)
	end := start.AddDate(0, 0, 1).Add(-time.Millisecond)

	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/juegos/api/calendar"
	endpoint.RawPath = ""
	query := url.Values{}
	query.Set("date", start.Format("01/02/2006"))
	query.Set("daysFromNow", "0")
	query.Set("startDate", strconv.FormatInt(start.UnixMilli(), 10))
	query.Set("endDate", strconv.FormatInt(end.UnixMilli(), 10))
	endpoint.RawQuery = query.Encode()
	return endpoint, nil
}

func readErrorBody(body io.Reader) (string, error) {
	payload, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes+1))
	if len(payload) > int(maxErrorBodyBytes) {
		payload = append(payload[:maxErrorBodyBytes], '.', '.', '.')
	}
	return strings.TrimSpace(string(payload)), err
}

func loadDefaultCalendarLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		return location
	}
	return time.FixedZone("Asia/Shanghai", 8*60*60)
}
