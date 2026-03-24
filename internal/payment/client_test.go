package payment

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"
)

func TestChargeValidationPanic(t *testing.T) {
	t.Setenv(validationPanicEnvKey, "true")

	client := NewClient(Config{
		BaseURL: "http://127.0.0.1:65535",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  noop.NewTracerProvider().Tracer("test"),
	})

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected validation panic")
		}
	}()

	_ = client.Charge(context.Background(), "ord-validation", validationPanicAmount, "mockpay")
}

func TestChargeDirectLineBug(t *testing.T) {
	client := NewClient(Config{
		BaseURL: "http://127.0.0.1:65535",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  noop.NewTracerProvider().Tracer("test"),
	})

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected direct line bug panic")
		}
	}()

	ctx := WithDirectLineBug(context.Background(), true)
	_ = client.Charge(ctx, "ord-direct-line", directLineBugAmount, "mockpay")
}

func TestChargeSuccessSkipsValidationPanic(t *testing.T) {
	t.Setenv(validationPanicEnvKey, "false")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"succeeded","authorization_id":"pay-123"}`))
	}))
	defer server.Close()

	client := NewClient(Config{
		BaseURL: server.URL,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  noop.NewTracerProvider().Tracer("test"),
	})

	if err := client.Charge(context.Background(), "ord-normal", 88.8, "mockpay"); err != nil {
		t.Fatalf("Charge() unexpected error: %v", err)
	}
}

func TestChargeBackgroundAutoBug(t *testing.T) {
	client := NewClient(Config{
		BaseURL: "http://127.0.0.1:65535",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  noop.NewTracerProvider().Tracer("test"),
	})

	err := client.Charge(context.Background(), BackgroundChargeFailedOrderPrefix()+"-test", 999.93, "mockpay")
	if err != ErrCharge {
		t.Fatalf("Charge() error = %v, want %v", err, ErrCharge)
	}
}
