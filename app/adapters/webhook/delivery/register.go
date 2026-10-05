package delivery

import "github.com/macrowallets/waas/app/services/webhook"

func init() {
	webhook.SetDeliveryClient(func() webhook.DeliveryClient {
		return NewClient()
	})
}
