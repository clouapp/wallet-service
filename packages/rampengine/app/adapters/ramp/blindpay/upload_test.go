package blindpay_test

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

func TestUpload_PostsTheFileUnderTheInstance(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotBucket, gotFilename, gotPartType string
	var gotBytes []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		require.NoError(t, err)
		reader := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "bucket":
				gotBucket = string(data)
			case "file":
				gotFilename, gotPartType, gotBytes = part.FileName(), part.Header.Get("Content-Type"), data
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"file_url":"https://files.blindpay.com/onboarding/abc.png"}`)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{APIKey: "bp_key", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})

	url, err := client.Upload(context.Background(), "onboarding", "id_doc_front.png", "image/png", []byte("\x89PNGdata"))
	require.NoError(t, err)
	assert.Equal(t, "https://files.blindpay.com/onboarding/abc.png", url)
	assert.Equal(t, "/v1/upload", gotPath)
	assert.Equal(t, "instance_id=in_000000000001", gotQuery)
	assert.Equal(t, "Bearer bp_key", gotAuth)
	assert.Equal(t, "onboarding", gotBucket)
	assert.Equal(t, "id_doc_front.png", gotFilename)
	assert.Equal(t, "image/png", gotPartType)
	assert.Equal(t, []byte("\x89PNGdata"), gotBytes)
}

func TestUpload_RefusesAnAnswerThatIsNotAnHTTPSURL(t *testing.T) {
	for _, body := range []string{`{"file_url":""}`, `{"file_url":"http://files.blindpay.com/x"}`, `{"file_url":"not a url"}`, `{}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})
		_, err := client.Upload(context.Background(), "onboarding", "f.png", "image/png", []byte("x"))
		assert.ErrorIs(t, err, ramp.ErrProviderFailed, body)
		server.Close()
	}
}

func TestUpload_ClassifiesARejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"success":false,"code":"AUTH_UNAUTHORIZED","message":"","trace_id":"t"}`)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})
	_, err := client.Upload(context.Background(), "onboarding", "f.png", "image/png", []byte("x"))
	assert.ErrorIs(t, err, ramp.ErrProviderCredentialsInvalid)
}

func TestUpload_RefusesAnUnusableInstanceBeforeSending(t *testing.T) {
	client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: "in_/../x", BaseURL: "http://127.0.0.1:1/v1"})
	_, err := client.Upload(context.Background(), "onboarding", "f.png", "image/png", []byte("x"))
	assert.ErrorIs(t, err, ramp.ErrRequestNotSent)
}
