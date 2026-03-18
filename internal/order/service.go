package order

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/payment"
	"github.com/Xmandon/xdom/internal/repository"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Config struct {
	ServiceName     string
	Environment     string
	Version         string
	CommitSHA       string
	OrderTimeoutSec int
	Repository      *repository.SQLiteRepository
	PaymentClient   *payment.Client
	Faults          *faults.State
	Logger          *slog.Logger
	Tracer          oteltrace.Tracer
	Metrics         *telemetry.Manager
}

type Service struct {
	cfg Config
}

type CreateOrderInput struct {
	UserID         string  `json:"user_id"`
	SKU            string  `json:"sku"`
	Quantity       int     `json:"quantity"`
	Amount         float64 `json:"amount"`
	PaymentChannel string  `json:"payment_channel"`
}

type OrderResponse struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	SKU            string    `json:"sku"`
	Quantity       int       `json:"quantity"`
	Amount         float64   `json:"amount"`
	Status         string    `json:"status"`
	PaymentChannel string    `json:"payment_channel"`
	LastError      string    `json:"last_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) CreateOrder(ctx context.Context, input CreateOrderInput) (OrderResponse, error) {
	started := time.Now()
	ctx, span := s.cfg.Tracer.Start(ctx, "order.create")
	defer span.End()
	span.SetAttributes(
		attribute.String("service.name", s.cfg.ServiceName),
		attribute.String("user.id", input.UserID),
		attribute.String("inventory.sku", input.SKU),
		attribute.String("payment.channel", input.PaymentChannel),
		attribute.String("release.version", s.cfg.Version),
		attribute.String("commit.sha", s.cfg.CommitSHA),
	)

	orderID := fmt.Sprintf("ord-%d", time.Now().UnixNano())
	rec := repository.OrderRecord{
		ID:             orderID,
		UserID:         input.UserID,
		SKU:            input.SKU,
		Quantity:       input.Quantity,
		Amount:         input.Amount,
		Status:         "pending",
		PaymentChannel: input.PaymentChannel,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(time.Duration(s.cfg.OrderTimeoutSec) * time.Second),
	}

	if err := s.cfg.Repository.CreatePendingOrder(ctx, rec); err != nil {
		s.cfg.Metrics.RecordInventoryFailure(ctx, err.Error())
		span.RecordError(err)
		return OrderResponse{}, err
	}

	if err := s.cfg.PaymentClient.Charge(ctx, rec.ID, rec.Amount, rec.PaymentChannel); err != nil {
		span.RecordError(err)
		mode, _ := s.cfg.Faults.Get()
		if mode == faults.PaymentTimeout {
			span.AddEvent("payment left order pending for worker reconciliation")
			s.cfg.Metrics.RecordPaymentTimeout(ctx)
			attrs := append([]slog.Attr{
				slog.String("order_id", rec.ID),
				slog.String("status", rec.Status),
				slog.String("fault_mode", string(mode)),
			}, telemetry.TraceLogAttrs(ctx)...)
			s.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "order left pending after payment timeout", attrs...)
			return toResponse(rec), nil
		}
		s.cfg.Metrics.RecordPaymentFailure(ctx, err.Error())
		_ = s.cfg.Repository.CancelOrder(ctx, rec.ID, "payment_failed")
		rec.Status = "failed"
		rec.LastError = err.Error()
		attrs := append([]slog.Attr{
			slog.String("order_id", rec.ID),
			slog.String("status", rec.Status),
			slog.String("error", err.Error()),
		}, telemetry.TraceLogAttrs(ctx)...)
		s.cfg.Logger.LogAttrs(ctx, slog.LevelError, "order payment failed", attrs...)
		return toResponse(rec), err
	}

	rec.Status = "paid"
	rec.UpdatedAt = time.Now().UTC()
	if err := s.cfg.Repository.UpdateOrderStatus(ctx, rec.ID, "paid", ""); err != nil {
		span.RecordError(err)
		return OrderResponse{}, err
	}
	s.cfg.Metrics.RecordOrderCreated(ctx, rec.PaymentChannel, time.Since(started))
	s.cfg.Metrics.RecordOrderPaid(ctx, rec.PaymentChannel)
	attrs := append([]slog.Attr{
		slog.String("order_id", rec.ID),
		slog.String("status", rec.Status),
		slog.String("payment_channel", rec.PaymentChannel),
	}, telemetry.TraceLogAttrs(ctx)...)
	s.cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "order created", attrs...)
	return toResponse(rec), nil
}

func (s *Service) GetOrder(ctx context.Context, orderID string) (OrderResponse, error) {
	rec, err := s.cfg.Repository.GetOrder(ctx, orderID)
	if err != nil {
		return OrderResponse{}, err
	}
	return toResponse(rec), nil
}

func (s *Service) CancelOrder(ctx context.Context, orderID string, reason string) (OrderResponse, error) {
	ctx, span := s.cfg.Tracer.Start(ctx, "order.cancel")
	defer span.End()
	span.SetAttributes(attribute.String("order.id", orderID))

	if err := s.cfg.Repository.CancelOrder(ctx, orderID, reason); err != nil {
		span.RecordError(err)
		return OrderResponse{}, err
	}
	s.cfg.Metrics.RecordOrderCancelled(ctx, reason)
	attrs := append([]slog.Attr{
		slog.String("order_id", orderID),
		slog.String("reason", reason),
	}, telemetry.TraceLogAttrs(ctx)...)
	s.cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "order cancelled", attrs...)
	rec, err := s.cfg.Repository.GetOrder(ctx, orderID)
	if err != nil {
		return OrderResponse{}, err
	}
	return toResponse(rec), nil
}

func (s *Service) ListInventory(ctx context.Context) ([]repository.InventoryRecord, error) {
	return s.cfg.Repository.ListInventory(ctx)
}

func (s *Service) CancelExpiredOrders(ctx context.Context) error {
	ctx, span := s.cfg.Tracer.Start(ctx, "worker.cancel_expired_orders")
	defer span.End()

	expired, err := s.cfg.Repository.ListExpiredPendingOrders(ctx, time.Now().UTC())
	if err != nil {
		span.RecordError(err)
		return err
	}
	for _, rec := range expired {
		if err := s.cfg.Repository.CancelOrder(ctx, rec.ID, "expired"); err != nil {
			s.cfg.Metrics.RecordWorkerFailed(ctx, "cancel_expired_failed")
			span.RecordError(err)
			return err
		}
		s.cfg.Metrics.RecordOrderCancelled(ctx, "expired")
		s.cfg.Metrics.RecordWorkerProcessed(ctx, "expired_cancelled")
	}
	return nil
}

func toResponse(rec repository.OrderRecord) OrderResponse {
	return OrderResponse{
		ID:             rec.ID,
		UserID:         rec.UserID,
		SKU:            rec.SKU,
		Quantity:       rec.Quantity,
		Amount:         rec.Amount,
		Status:         rec.Status,
		PaymentChannel: rec.PaymentChannel,
		LastError:      rec.LastError,
		CreatedAt:      rec.CreatedAt,
		UpdatedAt:      rec.UpdatedAt,
		ExpiresAt:      rec.ExpiresAt,
	}
}
