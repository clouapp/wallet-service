package blindpay_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), []byte("\x00\x00\x00\rIHDRfake-png-body")...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), []byte("fake-jpeg-body")...)
	gifBytes  = []byte("GIF89a fake-gif-body")
)

func dataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestParseImageDataURI_AcceptsJPEGAndPNG(t *testing.T) {
	mime, data, err := blindpay.ParseImageDataURI(dataURI("image/png", pngBytes))
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.Equal(t, pngBytes, data)

	mime, data, err = blindpay.ParseImageDataURI(dataURI("image/jpeg", jpegBytes))
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", mime)
	assert.Equal(t, jpegBytes, data)
}

func TestParseImageDataURI_Refusals(t *testing.T) {
	big := append(append([]byte{}, pngBytes...), make([]byte, 10<<20)...)
	cases := map[string]string{
		"not a data URI":        "https://example.com/a.png",
		"no payload":            "data:image/png;base64",
		"not base64 encoded":    "data:image/png," + string(pngBytes),
		"gif":                   dataURI("image/gif", gifBytes),
		"declared png, is jpeg": dataURI("image/png", jpegBytes),
		"declared jpeg, is gif": dataURI("image/jpeg", gifBytes),
		"bad base64":            "data:image/png;base64,!!!",
		"empty":                 "data:image/png;base64,",
		"over 10 MB":            dataURI("image/png", big),
	}
	for name, uri := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := blindpay.ParseImageDataURI(uri)
			require.ErrorIs(t, err, ramp.ErrKycDataInvalid)
			assert.False(t, strings.Contains(err.Error(), "fake"), "the error never restates the image")
		})
	}
}
