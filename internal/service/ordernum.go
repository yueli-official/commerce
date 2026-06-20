// Package service implements the commerce domain logic.
package service

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// NewOrderNo generates a unique order number for use as the Alipay out_trade_no.
// Format: "CM" + yyyyMMddHHmmss + 8 random alphanumeric chars (≤ 64 ASCII alnum).
func NewOrderNo() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	ts := time.Now().UTC().Format("20060102150405")
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.IntN(len(chars))]
	}
	return fmt.Sprintf("CM%s%s", ts, b)
}
