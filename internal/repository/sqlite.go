package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrInventoryConflict = errors.New("inventory conflict")
)

type Config struct {
	DBPath  string
	Logger  *slog.Logger
	Faults  *faults.State
	Tracer  oteltrace.Tracer
	Metrics *telemetry.Manager
}

type SQLiteRepository struct {
	db      *sql.DB
	logger  *slog.Logger
	faults  *faults.State
	tracer  oteltrace.Tracer
	metrics *telemetry.Manager
}

type OrderRecord struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	SKU            string    `json:"sku"`
	Quantity       int       `json:"quantity"`
	Amount         float64   `json:"amount"`
	Status         string    `json:"status"`
	PaymentChannel string    `json:"payment_channel"`
	LastError      string    `json:"last_error"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type InventoryRecord struct {
	SKU       string `json:"sku"`
	Available int    `json:"available"`
}

func NewSQLiteRepository(ctx context.Context, cfg Config) (*SQLiteRepository, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return &SQLiteRepository{
		db:      db,
		logger:  cfg.Logger,
		faults:  cfg.Faults,
		tracer:  cfg.Tracer,
		metrics: cfg.Metrics,
	}, nil
}

func (r *SQLiteRepository) Init(ctx context.Context) error {
	_, span := r.tracer.Start(ctx, "repository.init")
	defer span.End()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS orders (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			sku TEXT NOT NULL,
			quantity INTEGER NOT NULL,
			amount REAL NOT NULL,
			status TEXT NOT NULL,
			payment_channel TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS inventory (
			sku TEXT PRIMARY KEY,
			available INTEGER NOT NULL
		);`,
	}
	for _, stmt := range statements {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			span.RecordError(err)
			return err
		}
	}
	return nil
}

func (r *SQLiteRepository) SeedInventory(ctx context.Context) error {
	_, span := r.tracer.Start(ctx, "repository.seed_inventory")
	defer span.End()

	items := []InventoryRecord{
		{SKU: "sku-book", Available: 100},
		{SKU: "sku-card", Available: 80},
		{SKU: "sku-bundle", Available: 30},
	}
	for _, item := range items {
		if _, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO inventory (sku, available) VALUES (?, ?)`, item.SKU, item.Available); err != nil {
			span.RecordError(err)
			return err
		}
	}
	return nil
}

func (r *SQLiteRepository) CreatePendingOrder(ctx context.Context, order OrderRecord) error {
	ctx, span := r.tracer.Start(ctx, "repository.create_pending_order")
	defer span.End()
	span.SetAttributes(attribute.String("order.id", order.ID), attribute.String("inventory.sku", order.SKU))

	if err := r.reserveInventory(ctx, order.SKU, order.Quantity); err != nil {
		span.RecordError(err)
		return err
	}
	if err := r.maybeDelayOrFailWrite(); err != nil {
		_ = r.releaseInventory(ctx, order.SKU, order.Quantity)
		span.RecordError(err)
		return err
	}
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO orders (id, user_id, sku, quantity, amount, status, payment_channel, created_at, updated_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		order.ID,
		order.UserID,
		order.SKU,
		order.Quantity,
		order.Amount,
		order.Status,
		order.PaymentChannel,
		order.CreatedAt,
		order.UpdatedAt,
		order.ExpiresAt,
	)
	if err != nil {
		_ = r.releaseInventory(ctx, order.SKU, order.Quantity)
		span.RecordError(err)
		return err
	}
	return nil
}

func (r *SQLiteRepository) UpdateOrderStatus(ctx context.Context, orderID string, status string, lastError string) error {
	ctx, span := r.tracer.Start(ctx, "repository.update_order_status")
	defer span.End()
	span.SetAttributes(attribute.String("order.id", orderID), attribute.String("order.status", status))

	if err := r.maybeDelayOrFailWrite(); err != nil {
		span.RecordError(err)
		return err
	}
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE orders SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		status,
		lastError,
		time.Now().UTC(),
		orderID,
	)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (r *SQLiteRepository) GetOrder(ctx context.Context, orderID string) (OrderRecord, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_order")
	defer span.End()
	span.SetAttributes(attribute.String("order.id", orderID))

	if err := r.maybeSlowQuery(); err != nil {
		attrs := append([]slog.Attr{slog.String("order_id", orderID)}, telemetry.TraceLogAttrs(ctx)...)
		r.logger.LogAttrs(ctx, slog.LevelWarn, "slow query injected", attrs...)
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, user_id, sku, quantity, amount, status, payment_channel, last_error, created_at, updated_at, expires_at FROM orders WHERE id = ?`, orderID)
	var rec OrderRecord
	if err := row.Scan(&rec.ID, &rec.UserID, &rec.SKU, &rec.Quantity, &rec.Amount, &rec.Status, &rec.PaymentChannel, &rec.LastError, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrderRecord{}, ErrNotFound
		}
		span.RecordError(err)
		return OrderRecord{}, err
	}
	return rec, nil
}

func (r *SQLiteRepository) CancelOrder(ctx context.Context, orderID string, reason string) error {
	rec, err := r.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if rec.Status == "cancelled" || rec.Status == "paid" {
		return nil
	}
	if err := r.releaseInventory(ctx, rec.SKU, rec.Quantity); err != nil {
		return err
	}
	return r.UpdateOrderStatus(ctx, orderID, "cancelled", reason)
}

func (r *SQLiteRepository) ListInventory(ctx context.Context) ([]InventoryRecord, error) {
	ctx, span := r.tracer.Start(ctx, "repository.list_inventory")
	defer span.End()

	rows, err := r.db.QueryContext(ctx, `SELECT sku, available FROM inventory ORDER BY sku`)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer rows.Close()

	items := make([]InventoryRecord, 0)
	for rows.Next() {
		var item InventoryRecord
		if err := rows.Scan(&item.SKU, &item.Available); err != nil {
			span.RecordError(err)
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *SQLiteRepository) ListExpiredPendingOrders(ctx context.Context, before time.Time) ([]OrderRecord, error) {
	ctx, span := r.tracer.Start(ctx, "repository.list_expired_pending_orders")
	defer span.End()

	rows, err := r.db.QueryContext(ctx, `SELECT id, user_id, sku, quantity, amount, status, payment_channel, last_error, created_at, updated_at, expires_at FROM orders WHERE status = 'pending' AND expires_at <= ?`, before)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer rows.Close()

	items := make([]OrderRecord, 0)
	for rows.Next() {
		var item OrderRecord
		if err := rows.Scan(&item.ID, &item.UserID, &item.SKU, &item.Quantity, &item.Amount, &item.Status, &item.PaymentChannel, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.ExpiresAt); err != nil {
			span.RecordError(err)
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *SQLiteRepository) CountActivePendingOrders(ctx context.Context) (int64, error) {
	row := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE status = 'pending'`)
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *SQLiteRepository) reserveInventory(ctx context.Context, sku string, qty int) error {
	ctx, span := r.tracer.Start(ctx, "repository.reserve_inventory")
	defer span.End()
	span.SetAttributes(attribute.String("inventory.sku", sku), attribute.Int("inventory.quantity", qty))

	if err := r.maybeSlowQuery(); err != nil {
		attrs := append([]slog.Attr{slog.String("sku", sku)}, telemetry.TraceLogAttrs(ctx)...)
		r.logger.LogAttrs(ctx, slog.LevelWarn, "reserve inventory delayed", attrs...)
	}
	mode, _ := r.faults.Get()
	if mode == faults.InventoryConflict {
		span.SetAttributes(attribute.String("fault.mode", string(mode)))
		span.RecordError(ErrInventoryConflict)
		span.SetStatus(codes.Error, "inventory_conflict")
		span.AddEvent("fault.injected", oteltrace.WithAttributes(
			attribute.String("fault.mode", string(mode)),
			attribute.String("inventory.sku", sku),
		))
		attrs := append([]slog.Attr{
			slog.String("sku", sku),
			slog.Int("quantity", qty),
			slog.String("fault_mode", string(mode)),
		}, telemetry.TraceLogAttrs(ctx)...)
		r.logger.LogAttrs(ctx, slog.LevelWarn, "inventory conflict injected", attrs...)
		return ErrInventoryConflict
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var available int
	if err := tx.QueryRowContext(ctx, `SELECT available FROM inventory WHERE sku = ?`, sku).Scan(&available); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInventoryConflict
		}
		return err
	}
	if available < qty {
		return ErrInventoryConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inventory SET available = available - ? WHERE sku = ?`, qty, sku); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteRepository) releaseInventory(ctx context.Context, sku string, qty int) error {
	ctx, span := r.tracer.Start(ctx, "repository.release_inventory")
	defer span.End()
	_, err := r.db.ExecContext(ctx, `UPDATE inventory SET available = available + ? WHERE sku = ?`, qty, sku)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (r *SQLiteRepository) maybeSlowQuery() error {
	mode, delayMS := r.faults.Get()
	if mode == faults.DBSlowQuery {
		time.Sleep(time.Duration(delayMS) * time.Millisecond)
		return errors.New("slow query injected")
	}
	return nil
}

func (r *SQLiteRepository) maybeDelayOrFailWrite() error {
	mode, delayMS := r.faults.Get()
	if mode == faults.DBSlowQuery {
		time.Sleep(time.Duration(delayMS) * time.Millisecond)
	}
	if mode == faults.DBWriteError {
		return fmt.Errorf("db write error injected")
	}
	return nil
}
