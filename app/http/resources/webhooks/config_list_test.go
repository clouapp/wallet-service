package webhooks_test

import (
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/http/resources/webhooks"
	"github.com/macrowallets/waas/app/models"
)

func TestConfigList_Keeps_TheDataEnvelope(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		configs []models.WebhookConfig
		want    string
	}{
		"nil page":   {nil, `{"data":null}`},
		"empty page": {[]models.WebhookConfig{}, `{"data":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(webhooks.NewConfigList(tc.configs))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("wire = %s", raw)
			}
		})
	}
}
