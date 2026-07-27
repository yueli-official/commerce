package controller

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"

	"github.com/yueli-official/commerce/paykit"
	"github.com/yueli-official/commerce/internal/paymentrecovery"
	"github.com/yueli-official/commerce/internal/service"
)

type DisputeWebhook struct {
	provider string
	merchant string
	verifier paykit.VerifyDisputeProvider
	service  *service.Service
}

func NewDisputeWebhook(
	provider string,
	verifier paykit.VerifyDisputeProvider,
	svc *service.Service,
) *DisputeWebhook {
	return &DisputeWebhook{
		provider: strings.TrimSpace(provider),
		merchant: "primary",
		verifier: verifier,
		service:  svc,
	}
}

func (controller *DisputeWebhook) Handle(request *ghttp.Request) {
	body, err := io.ReadAll(io.LimitReader(
		request.Body, maxProviderNotifyBodyBytes+1,
	))
	if err != nil || len(body) > maxProviderNotifyBodyBytes ||
		controller.verifier == nil || controller.service == nil {
		controller.writeFailure(request, http.StatusBadRequest)
		return
	}
	headers := make(map[string]string, len(request.Header))
	for key := range request.Header {
		headers[key] = request.Header.Get(key)
	}
	out, err := controller.verifier.VerifyDispute(
		request.Context(), body, headers,
	)
	if err != nil || out == nil {
		controller.writeFailure(request, http.StatusBadRequest)
		return
	}
	status, ok := recoveryDisputeStatus(out.Status)
	if !ok || strings.TrimSpace(out.EventID) == "" {
		controller.writeFailure(request, http.StatusBadRequest)
		return
	}
	occurredAt := out.ObservedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	_, err = controller.service.AcceptDisputeObservation(
		request.Context(),
		paymentrecovery.DisputeObservation{
			Status: status, Provider: controller.provider,
			Merchant:          controller.merchant,
			ProviderTxID:      out.ProviderTxID,
			ProviderDisputeID: out.ProviderDisputeID,
			ProviderStatus:    out.ProviderStatus,
			OutcomeCode:       out.OutcomeCode,
			Money: paymentrecovery.Money{
				AmountCents: out.AmountCents, Currency: out.Currency,
			},
			ReasonCode: out.ReasonCode, OpenedAt: out.OpenedAt,
			DueAt: out.DueAt, Source: paymentrecovery.SourceCallback,
			Authoritative: true, IdempotencyKey: out.EventID,
			PayloadDigest: paymentrecovery.DigestPayload(body),
			OccurredAt:    occurredAt,
		},
	)
	if err != nil {
		controller.writeFailure(request, http.StatusInternalServerError)
		return
	}
	request.Response.Status = http.StatusOK
	request.Response.WriteJson(map[string]string{"status": "accepted"})
}

func recoveryDisputeStatus(
	status paykit.DisputeStatus,
) (paymentrecovery.DisputeStatus, bool) {
	switch status {
	case paykit.DisputeStatusOpen:
		return paymentrecovery.DisputeOpen, true
	case paykit.DisputeStatusNeedsResponse:
		return paymentrecovery.DisputeNeedsResponse, true
	case paykit.DisputeStatusUnderReview:
		return paymentrecovery.DisputeUnderReview, true
	case paykit.DisputeStatusWon:
		return paymentrecovery.DisputeWon, true
	case paykit.DisputeStatusLost:
		return paymentrecovery.DisputeLost, true
	case paykit.DisputeStatusAccepted:
		return paymentrecovery.DisputeAccepted, true
	case paykit.DisputeStatusClosed:
		return paymentrecovery.DisputeClosed, true
	default:
		return "", false
	}
}

func (controller *DisputeWebhook) writeFailure(
	request *ghttp.Request,
	status int,
) {
	request.Response.Status = status
	request.Response.WriteJson(map[string]string{"status": "rejected"})
}
