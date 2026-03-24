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
	t.Setenv(directLineBugEnvKey, "true")

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

	_ = client.Charge(context.Background(), "ord-direct-line", directLineBugAmount, "mockpay")
}

func TestChargeSuccessSkipsValidationPanic(t *testing.T) {
	t.Setenv(validationPanicEnvKey, "false")
	t.Setenv(directLineBugEnvKey, "false")

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
