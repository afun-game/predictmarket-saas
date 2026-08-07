package lmb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClientUsesLMBBaseURL(t *testing.T) {
	t.Parallel()

	if got := NewClient().baseURL; got != DefaultBaseURL {
		t.Errorf("base URL = %q, want %q", got, DefaultBaseURL)
	}
}

func TestCalendarBuildsDefaultAsiaShanghaiCalendarDateRequest(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/juegos/api/calendar" {
			t.Errorf("path = %q, want %q", got, "/juegos/api/calendar")
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		query := r.URL.Query()
		assertQueryValue(t, query.Get("date"), "08/07/2026")
		assertQueryValue(t, query.Get("daysFromNow"), "0")
		assertQueryValue(t, query.Get("startDate"), "1786032000000")
		assertQueryValue(t, query.Get("endDate"), "1786118399999")
		if got := len(query); got != 4 {
			t.Errorf("query parameters = %d, want 4", got)
		}
		_, _ = fmt.Fprint(w, `{"games_info":[]}`)
	}))
	defer server.Close()

	games, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())).Calendar(context.Background(), day)
	if err != nil {
		t.Fatalf("Calendar() error = %v", err)
	}
	if len(games) != 0 {
		t.Errorf("games = %#v, want no games", games)
	}
}

func TestCalendarRejectsMissingGamesInfo(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"{}", `{"games_info":null}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, body)
			}))
			defer server.Close()

			_, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())).Calendar(context.Background(), time.Now())
			if !errors.Is(err, ErrMissingGamesInfo) {
				t.Fatalf("Calendar() error = %v, want ErrMissingGamesInfo", err)
			}
		})
	}
}

func TestCalendarRejectsNonHTTPBaseURL(t *testing.T) {
	t.Parallel()

	_, err := NewClient(WithBaseURL("ftp://lmb.example")).Calendar(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("Calendar() error = %v, want non-HTTP base URL error", err)
	}
}

func TestCalendarUsesConfiguredLocationWithInputCalendarDate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		assertQueryValue(t, query.Get("date"), "08/07/2026")
		assertQueryValue(t, query.Get("startDate"), "1786060800000")
		assertQueryValue(t, query.Get("endDate"), "1786147199999")
		_, _ = fmt.Fprint(w, `{"games_info":[]}`)
	}))
	defer server.Close()

	_, err := NewClient(
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
		WithCalendarLocation(time.UTC),
	).Calendar(context.Background(), time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Calendar() error = %v", err)
	}
}

func TestCalendarKeepsInputCalendarDateAcrossLocationOffset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		assertQueryValue(t, query.Get("date"), "08/07/2026")
		assertQueryValue(t, query.Get("startDate"), "1786060800000")
		assertQueryValue(t, query.Get("endDate"), "1786147199999")
		_, _ = fmt.Fprint(w, `{"games_info":[]}`)
	}))
	defer server.Close()

	inputLocation := time.FixedZone("UTC+08", 8*60*60)
	_, err := NewClient(
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
		WithCalendarLocation(time.UTC),
	).Calendar(
		context.Background(),
		time.Date(2026, time.August, 7, 0, 0, 0, 0, inputLocation),
	)
	if err != nil {
		t.Fatalf("Calendar() error = %v", err)
	}
}

func TestCalendarDecodesVerifiedFixture(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("testdata/calendar.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/juegos/api/calendar" {
			t.Errorf("path = %q, want %q", got, "/juegos/api/calendar")
		}
		assertQueryValue(t, r.URL.Query().Get("date"), "08/07/2026")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	games, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())).Calendar(
		context.Background(),
		time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Calendar() error = %v", err)
	}
	if got := len(games); got != 12 {
		t.Fatalf("games = %d, want 12", got)
	}
	if games[0].GameID != 846703 {
		t.Fatalf("first game ID = %d, want 846703", games[0].GameID)
	}
	if got := games[0].AwayTeam.TeamUUID; got != "88e08b39-0215-4599-8245-bd19939da7c2" {
		t.Errorf("first away team UUID = %q, want Saraperos UUID", got)
	}

	game := gameByID(games, 846703)
	if game == nil {
		t.Fatalf("game 846703 not found in %#v", games)
	}
	if game.Status != "F" || game.DetailedStatus != "Final" {
		t.Errorf("game status = %q/%q, want F/Final", game.Status, game.DetailedStatus)
	}
	if game.DateTime != 1786053600 {
		t.Errorf("game date time = %d, want 1786053600", game.DateTime)
	}
	if game.AwayTeam.Name != "Saraperos" || game.AwayTeam.ShortName != "SLW" {
		t.Errorf("away team = %#v, want Saraperos (SLW)", game.AwayTeam)
	}
	if game.LocalTeam.Name != "Algodoneros" || game.LocalTeam.ShortName != "LAG" {
		t.Errorf("local team = %#v, want Algodoneros (LAG)", game.LocalTeam)
	}
}

func TestCalendarReturnsHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())).Calendar(context.Background(), time.Now())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Calendar() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests || httpErr.Body != "rate limited" {
		t.Errorf("HTTPError = %#v", httpErr)
	}
}

func TestCalendarPreservesErrorBodyReadFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("read failed")
	httpClient := &http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(errorReader{err: want}),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := NewClient(WithBaseURL("https://lmb.example"), WithHTTPClient(httpClient)).Calendar(context.Background(), time.Now())
	if !errors.Is(err, want) {
		t.Fatalf("Calendar() error = %v, want body read error", err)
	}
}

func TestCalendarReturnsMalformedJSONError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"games_info":[}`)
	}))
	defer server.Close()

	_, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())).Calendar(context.Background(), time.Now())
	if err == nil {
		t.Fatal("Calendar() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "decode LMB calendar") {
		t.Errorf("Calendar() error = %q, want decode context", err)
	}
}

func TestCalendarRejectsAdvertisedResponseOverFourMiB(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxResponseBodyBytes+1, 10))
		_, _ = fmt.Fprint(w, "{")
	}))
	defer server.Close()

	_, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())).Calendar(context.Background(), time.Now())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Calendar() error = %v, want ErrResponseTooLarge", err)
	}
}

func TestCalendarRejectsChunkedResponseOverFourMiB(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"games_info":[]}`)
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprint(w, strings.Repeat(" ", int(maxResponseBodyBytes)))
	}))
	defer server.Close()

	var contentLength atomic.Int64
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		response, err := server.Client().Transport.RoundTrip(request)
		if response != nil {
			contentLength.Store(response.ContentLength)
		}
		return response, err
	})}

	_, err := NewClient(WithBaseURL(server.URL), WithHTTPClient(httpClient)).Calendar(context.Background(), time.Now())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Calendar() error = %v, want ErrResponseTooLarge", err)
	}
	if got := contentLength.Load(); got != -1 {
		t.Errorf("ContentLength = %d, want unknown length -1", got)
	}
}

func gameByID(games []Game, gameID int64) *Game {
	for index := range games {
		if games[index].GameID == gameID {
			return &games[index]
		}
	}
	return nil
}

func assertQueryValue(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("query value = %q, want %q", got, want)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}
