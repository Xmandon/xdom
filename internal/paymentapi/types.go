package paymentapi

type ChargeRequest struct {
	OrderID string  `json:"order_id"`
	Amount  float64 `json:"amount"`
	Channel string  `json:"channel"`
}

type ChargeResponse struct {
	Status          string `json:"status"`
	AuthorizationID string `json:"authorization_id,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	Message         string `json:"message,omitempty"`
}

const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"

	ErrorCodeTimeout      = "payment_timeout"
	ErrorCodeChargeFailed = "payment_charge_failed"
)
