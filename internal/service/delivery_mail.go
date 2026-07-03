package service

import (
	"context"
	"strings"
)

type DeliveryMail struct {
	To          string
	OrderNo     string
	Title       string
	DeliveryRef string
	DeliveryURL string
}

type DeliveryMailer interface {
	SendDelivery(ctx context.Context, in DeliveryMail) error
}

func DeliveryEmailAvailable(in DeliveryMail) bool {
	return strings.TrimSpace(in.To) != ""
}
