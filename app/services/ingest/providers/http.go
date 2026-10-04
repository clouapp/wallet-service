package providers

import (
	"context"

	"github.com/macrowallets/waas/pkg/httpclient"
)

func exchange(ctx context.Context, client *httpclient.Client, method, rawURL string, header map[string]string, body []byte) (int, []byte, error) {
	resp, err := client.Do(ctx, httpclient.Request{
		Method:  method,
		URL:     rawURL,
		Header:  header,
		Body:    body,
		HasBody: body != nil,
	})
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, resp.Body, nil
}
