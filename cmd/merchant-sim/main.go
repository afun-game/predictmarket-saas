// Command merchant-sim is a local counterpart for the V3 seamless-wallet
// callback and settlement-webhook contracts. It is intentionally stateful so
// acceptance tests can exercise retries, duplicate delivery, and rollback
// arriving before the original debit. The simulator logic lives in
// internal/merchantsim and is also reused by the platform-side chaos tests.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/merchantsim"
)

func main() {
	addr := flag.String("addr", ":8090", "HTTP listen address")
	secret := flag.String("secret", "", "callback signing secret; empty disables signature verification")
	merchantID := flag.String("merchant-id", "", "expected X-PM-Merchant-Id; empty accepts any merchant")
	initialBalance := flag.String("initial-balance", "100.00", "initial balance for every user")
	failStatus := flag.String("fail-status", "", "return this callback status for every request (test fault injection)")
	failHTTPStatus := flag.Int("fail-http-status", 0, "return this HTTP status for every callback/webhook request (test 5xx injection)")
	failCount := flag.Int("fail-count", 0, "fail only the first N requests, then behave normally")
	delay := flag.Duration("delay", 0, "delay callback responses (test timeout injection)")
	flag.Parse()

	simulator, err := merchantsim.New(merchantsim.Options{
		Secret:         strings.TrimSpace(*secret),
		MerchantID:     strings.TrimSpace(*merchantID),
		InitialBalance: *initialBalance,
		FailStatus:     *failStatus,
		FailHTTPStatus: *failHTTPStatus,
		FailCount:      *failCount,
		Delay:          *delay,
	})
	if err != nil {
		slog.Error("configure merchant simulator", "error", err)
		os.Exit(1)
	}
	server := &http.Server{Addr: *addr, Handler: simulator.Handler()}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	slog.Info("merchant simulator listening", "addr", *addr, "callback", "/callback", "webhook", "/webhook")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("merchant simulator", "error", err)
		os.Exit(1)
	}
}
