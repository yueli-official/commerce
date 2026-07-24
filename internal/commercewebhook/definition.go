package commercewebhook

import (
	"github.com/yueli-official/foundation/go/webhook"
)

const (
	OrderFulfilled webhook.EventType = "com.yueli.commerce.order.fulfilled.v1"
	OrderRefunded  webhook.EventType = "com.yueli.commerce.order.refunded.v1"
)

func Definition(instance string) webhook.Definition {
	return webhook.Definition{
		Version:  webhook.DefinitionVersion,
		Consumer: "commerce",
		Source:   "urn:yueli:commerce:" + instance,
		EventTypes: []webhook.EventTypeDefinition{
			{Type: OrderFulfilled, MaxDataBytes: 64 << 10},
			{Type: OrderRefunded, MaxDataBytes: 64 << 10},
		},
	}
}
