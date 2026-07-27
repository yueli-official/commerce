package paykit

import "context"

type FakeProvider struct {
	name string

	NextCreate      CreatePaymentOut
	NextCapture     CapturePaymentOut
	NextNotify      NotifyOut
	NextQuery       QueryPaymentOut
	NextRefund      RefundOut
	NextRefundQuery QueryRefundOut
	NextDispute     DisputeOut

	CreateErr       error
	CaptureErr      error
	NotifyErr       error
	QueryErr        error
	RefundErr       error
	RefundQueryErr  error
	DisputeErr      error
	DisputeQueryErr error

	CreateCalls       []CreatePaymentIn
	CaptureCalls      []CapturePaymentIn
	NotifyCalls       []NotifyCall
	QueryCalls        []QueryPaymentIn
	RefundCalls       []RefundIn
	RefundQueryCalls  []QueryRefundIn
	DisputeCalls      []NotifyCall
	DisputeQueryCalls []string
}

type NotifyCall struct {
	Body    []byte
	Headers map[string]string
}

func NewFakeProvider(name string) *FakeProvider {
	return &FakeProvider{name: name}
}

func (p *FakeProvider) Name() string {
	return p.name
}

func (p *FakeProvider) CreatePayment(_ context.Context, in CreatePaymentIn) (*CreatePaymentOut, error) {
	p.CreateCalls = append(p.CreateCalls, in)
	if p.CreateErr != nil {
		return nil, p.CreateErr
	}
	out := p.NextCreate
	if out.Provider == "" {
		out.Provider = NormalizeProvider(p.name)
	}
	return &out, nil
}

func (p *FakeProvider) CapturePayment(_ context.Context, in CapturePaymentIn) (*CapturePaymentOut, error) {
	p.CaptureCalls = append(p.CaptureCalls, in)
	if p.CaptureErr != nil {
		return nil, p.CaptureErr
	}
	out := p.NextCapture
	return &out, nil
}

func (p *FakeProvider) VerifyNotify(_ context.Context, body []byte, headers map[string]string) (*NotifyOut, error) {
	bodyCopy := append([]byte(nil), body...)
	headersCopy := make(map[string]string, len(headers))
	for key, value := range headers {
		headersCopy[key] = value
	}
	p.NotifyCalls = append(p.NotifyCalls, NotifyCall{Body: bodyCopy, Headers: headersCopy})
	if p.NotifyErr != nil {
		return nil, p.NotifyErr
	}
	out := p.NextNotify
	return &out, nil
}

func (p *FakeProvider) QueryPayment(_ context.Context, in QueryPaymentIn) (*QueryPaymentOut, error) {
	p.QueryCalls = append(p.QueryCalls, in)
	if p.QueryErr != nil {
		return nil, p.QueryErr
	}
	out := p.NextQuery
	return &out, nil
}

func (p *FakeProvider) Refund(_ context.Context, in RefundIn) (*RefundOut, error) {
	p.RefundCalls = append(p.RefundCalls, in)
	if p.RefundErr != nil {
		return nil, p.RefundErr
	}
	out := p.NextRefund
	return &out, nil
}

func (p *FakeProvider) QueryRefund(_ context.Context, in QueryRefundIn) (*QueryRefundOut, error) {
	p.RefundQueryCalls = append(p.RefundQueryCalls, in)
	if p.RefundQueryErr != nil {
		return nil, p.RefundQueryErr
	}
	out := p.NextRefundQuery
	return &out, nil
}

func (p *FakeProvider) VerifyDispute(
	_ context.Context,
	body []byte,
	headers map[string]string,
) (*DisputeOut, error) {
	bodyCopy := append([]byte(nil), body...)
	headersCopy := make(map[string]string, len(headers))
	for key, value := range headers {
		headersCopy[key] = value
	}
	p.DisputeCalls = append(
		p.DisputeCalls,
		NotifyCall{Body: bodyCopy, Headers: headersCopy},
	)
	if p.DisputeErr != nil {
		return nil, p.DisputeErr
	}
	out := p.NextDispute
	return &out, nil
}

func (p *FakeProvider) QueryDispute(
	_ context.Context,
	disputeID string,
) (*DisputeOut, error) {
	p.DisputeQueryCalls = append(p.DisputeQueryCalls, disputeID)
	if p.DisputeQueryErr != nil {
		return nil, p.DisputeQueryErr
	}
	out := p.NextDispute
	return &out, nil
}
