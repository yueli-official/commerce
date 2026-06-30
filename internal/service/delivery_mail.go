package service

import (
	"context"
	"html"
	"strings"

	"platform/gokit/mail"
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

type deliveryMailSender struct {
	sender mail.Sender
}

func NewDeliveryMailSender(sender mail.Sender) DeliveryMailer {
	return &deliveryMailSender{sender: sender}
}

func (m *deliveryMailSender) SendDelivery(ctx context.Context, in DeliveryMail) error {
	if m == nil || m.sender == nil || strings.TrimSpace(in.To) == "" {
		return nil
	}
	subject := "你的虚拟商品已可交付"
	body := `<p>你的订单 ` + html.EscapeString(in.OrderNo) + ` 已完成。</p>` +
		`<p>商品: ` + html.EscapeString(in.Title) + `</p>` +
		`<p>交付资源: ` + html.EscapeString(in.DeliveryRef) + `</p>`
	if in.DeliveryURL != "" {
		body += `<p><a href="` + html.EscapeString(in.DeliveryURL) + `">打开交付链接</a></p>`
	}
	return m.sender.Send(ctx, in.To, subject, body)
}
