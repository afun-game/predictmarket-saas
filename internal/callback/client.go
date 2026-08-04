// Package callback delivers merchant wallet callbacks and settlement webhooks.
package callback

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
)

const (
	defaultTimeout   = 3 * time.Second
	maxResponseBytes = 1 << 20
)

// Status values returned by a merchant callback endpoint.
const (
	StatusOK                = "ok"
	StatusDuplicate         = "duplicate"
	StatusInsufficientFunds = "insufficient_funds"
	StatusUserNotFound      = "user_not_found"
	StatusUserBlocked       = "user_blocked"
)

// ErrPermanent indicates a merchant response that should not be retried.
var ErrPermanent = errors.New("permanent merchant callback failure")

// Client posts signed wallet callbacks and webhooks to merchant endpoints.
type Client struct {
	httpClient       *http.Client
	now              func() time.Time
	allowPrivateURLs bool
}

// NewClient constructs a callback client with a hard request timeout.
func NewClient(timeout time.Duration) *Client {
	return newClient(timeout, false)
}

func newClient(timeout time.Duration, allowPrivateURLs bool) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := &Client{
		httpClient: &http.Client{Timeout: timeout},
		now:        time.Now,
	}
	if allowPrivateURLs {
		client.allowPrivateURLs = true
		client.httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // sandbox only.
		}
	}
	return client
}

// CallbackRequest is the JSON body for debit/credit/rollback deliveries.
type CallbackRequest struct {
	CallbackID    string         `json:"callback_id"`
	Type          string         `json:"type"`
	TransactionID string         `json:"transaction_id"`
	UserID        string         `json:"user_id"`
	Currency      string         `json:"currency"`
	Amount        string         `json:"amount"`
	Reason        string         `json:"reason"`
	Ref           map[string]any `json:"ref,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// CallbackResponse is the merchant HTTP 200 body.
type CallbackResponse struct {
	Status  string `json:"status"`
	Balance string `json:"balance,omitempty"`
}

// DeliverCallback posts one wallet callback and classifies the merchant reply.
func (c *Client) DeliverCallback(
	ctx context.Context,
	endpoint string,
	merchantID string,
	secret string,
	request CallbackRequest,
) (*CallbackResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal callback request: %w", err)
	}
	responseBody, statusCode, err := c.postSigned(ctx, endpoint, merchantID, secret, body)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		if statusCode >= 400 && statusCode < 500 && statusCode != http.StatusTooManyRequests {
			return nil, fmt.Errorf("%w: merchant returned HTTP %d", ErrPermanent, statusCode)
		}
		return nil, fmt.Errorf("merchant returned HTTP %d", statusCode)
	}
	var response CallbackResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode merchant callback response: %w", err)
	}
	response.Status = strings.ToLower(strings.TrimSpace(response.Status))
	if response.Balance != "" {
		response.Balance, err = normalizeCallbackBalance(response.Balance)
		if err != nil {
			return &response, fmt.Errorf("merchant returned invalid balance: %w", err)
		}
	}
	switch response.Status {
	case StatusOK, StatusDuplicate, StatusInsufficientFunds:
		if response.Balance == "" {
			return &response, errors.New("merchant callback response is missing balance")
		}
	}
	switch response.Status {
	case StatusOK, StatusDuplicate:
		return &response, nil
	case StatusInsufficientFunds, StatusUserNotFound, StatusUserBlocked:
		return &response, fmt.Errorf("%w: %s", ErrPermanent, response.Status)
	default:
		return &response, fmt.Errorf("merchant returned unsupported status %q", response.Status)
	}
}

func normalizeCallbackBalance(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "0", "0.0", "0.00":
		return "0.00", nil
	}
	cents, err := fixed.CentsFromString(value)
	if err != nil {
		return "", err
	}
	return fixed.FormatCents(cents), nil
}

// VerificationRequest asks a merchant to echo a challenge to prove callback
// URL ownership (V3 §7.2).
type VerificationRequest struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
}

// VerificationResponse is the merchant HTTP 200 body for a verification request.
type VerificationResponse struct {
	Status    string `json:"status"`
	Challenge string `json:"challenge,omitempty"`
}

// DeliverVerification posts a callback-ownership challenge and returns the
// echoed challenge when the merchant proves ownership.
func (c *Client) DeliverVerification(
	ctx context.Context,
	endpoint string,
	merchantID string,
	secret string,
	challenge string,
) (string, error) {
	payload, err := json.Marshal(VerificationRequest{
		Type:      "callback.verify",
		Challenge: challenge,
	})
	if err != nil {
		return "", fmt.Errorf("marshal callback verification request: %w", err)
	}
	responseBody, statusCode, err := c.postSigned(ctx, endpoint, merchantID, secret, payload)
	if err != nil {
		return "", err
	}
	if statusCode < 200 || statusCode >= 300 {
		if statusCode >= 400 && statusCode < 500 && statusCode != http.StatusTooManyRequests {
			return "", fmt.Errorf("%w: merchant returned HTTP %d", ErrPermanent, statusCode)
		}
		return "", fmt.Errorf("merchant returned HTTP %d", statusCode)
	}
	var response VerificationResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("decode merchant verification response: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(response.Status)) != StatusOK {
		return "", fmt.Errorf("%w: merchant verification status %q", ErrPermanent, response.Status)
	}
	echoed := strings.TrimSpace(response.Challenge)
	if echoed == "" {
		return "", errors.New("merchant verification response is missing the challenge echo")
	}
	if echoed != challenge {
		return "", errors.New("merchant verification challenge was not echoed correctly")
	}
	return echoed, nil
}

// BalanceRequest is the read-only balance query callback body.
type BalanceRequest struct {
	CallbackID string    `json:"callback_id"`
	Type       string    `json:"type"`
	UserID     string    `json:"user_id"`
	Currency   string    `json:"currency"`
	CreatedAt  time.Time `json:"created_at"`
}

// DeliverBalanceQuery asks a seamless merchant for the current user balance
// (V3 §11.2). The query is idempotent and has no ledger side effects.
func (c *Client) DeliverBalanceQuery(
	ctx context.Context,
	endpoint string,
	merchantID string,
	secret string,
	userID string,
	currency string,
) (string, error) {
	body, err := json.Marshal(BalanceRequest{
		CallbackID: "balance-" + userID,
		Type:       "balance",
		UserID:     userID,
		Currency:   currency,
		CreatedAt:  c.now().UTC(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal balance query: %w", err)
	}
	responseBody, statusCode, err := c.postSigned(ctx, endpoint, merchantID, secret, body)
	if err != nil {
		return "", err
	}
	if statusCode < 200 || statusCode >= 300 {
		if statusCode >= 400 && statusCode < 500 && statusCode != http.StatusTooManyRequests {
			return "", fmt.Errorf("%w: merchant returned HTTP %d", ErrPermanent, statusCode)
		}
		return "", fmt.Errorf("merchant returned HTTP %d", statusCode)
	}
	var response CallbackResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("decode merchant balance response: %w", err)
	}
	response.Status = strings.ToLower(strings.TrimSpace(response.Status))
	switch response.Status {
	case StatusOK, StatusDuplicate:
		return strings.TrimSpace(response.Balance), nil
	case StatusUserNotFound, StatusUserBlocked:
		return "", fmt.Errorf("%w: %s", ErrPermanent, response.Status)
	default:
		return "", fmt.Errorf("merchant returned unsupported balance status %q", response.Status)
	}
}

// DeliverWebhook posts one settlement webhook. Any 2xx response is success.
func (c *Client) DeliverWebhook(
	ctx context.Context,
	endpoint string,
	merchantID string,
	secret string,
	payload []byte,
) error {
	_, statusCode, err := c.postSigned(ctx, endpoint, merchantID, secret, payload)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		if statusCode >= 400 && statusCode < 500 && statusCode != http.StatusTooManyRequests {
			return fmt.Errorf("%w: merchant returned HTTP %d", ErrPermanent, statusCode)
		}
		return fmt.Errorf("merchant returned HTTP %d", statusCode)
	}
	return nil
}

func (c *Client) postSigned(
	ctx context.Context,
	endpoint string,
	merchantID string,
	secret string,
	body []byte,
) ([]byte, int, error) {
	if !c.allowPrivateURLs {
		if err := validatePublicHTTPSURL(endpoint); err != nil {
			return nil, 0, err
		}
	}
	if strings.TrimSpace(secret) == "" {
		return nil, 0, errors.New("callback secret is required")
	}
	timestamp := fmt.Sprintf("%d", c.now().UTC().Unix())
	signature := signPayload(secret, timestamp, body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create callback request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-PM-Timestamp", timestamp)
	request.Header.Set("X-PM-Signature", signature)
	request.Header.Set("X-PM-Merchant-Id", merchantID)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("deliver merchant callback: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read merchant callback response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, 0, errors.New("merchant callback response exceeds 1 MiB")
	}
	return responseBody, response.StatusCode, nil
}

func signPayload(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func validatePublicHTTPSURL(rawURL string) error {
	value, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || value.Scheme != "https" || value.Host == "" {
		return errors.New("callback URL must be an absolute HTTPS URL")
	}
	host := value.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return errors.New("callback URL must target a public host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return errors.New("callback URL must not target a private IP")
		}
	}
	return nil
}
