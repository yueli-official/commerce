// Package recoveryops defines the provider-neutral operations view over
// recoverable Commerce cases. It owns no transaction truth.
package recoveryops

import "time"

const (
	KindPayment    = "payment"
	KindRefund     = "refund"
	KindDispute    = "dispute"
	KindAssetGrant = "asset_grant"
)

type Case struct {
	Kind         string     `json:"kind" orm:"kind"`
	ID           string     `json:"id" orm:"id"`
	OrderID      string     `json:"orderId" orm:"order_id"`
	OrderNo      string     `json:"orderNo" orm:"order_no"`
	Provider     string     `json:"provider" orm:"provider"`
	State        string     `json:"state" orm:"state"`
	Attempts     int        `json:"attempts" orm:"attempts"`
	LastError    string     `json:"lastError,omitempty" orm:"last_error"`
	NextActionAt *time.Time `json:"nextActionAt,omitempty" orm:"next_action_at"`
	CreatedAt    time.Time  `json:"createdAt" orm:"created_at"`
	UpdatedAt    time.Time  `json:"updatedAt" orm:"updated_at"`
}
