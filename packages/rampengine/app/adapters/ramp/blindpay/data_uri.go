package blindpay

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// maxImageBytes is the decoded size cap of one image. BlindPay states none
// for its upload; this keeps one submission from holding tens of megabytes of
// PII in memory and in the staging blob.
const maxImageBytes = 10 << 20

// imageExtensions are the image types BlindPay's id_doc_*_file and
// selfie_file accept (".jpg, .jpeg, .png"), and the extension each one is
// uploaded under.
var imageExtensions = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
}

// parseImageDataURI decodes a `data:image/jpeg;base64,…` or
// `data:image/png;base64,…` URI. The declared type must be what the bytes
// are: BlindPay rejects a mislabelled file only after the customer create,
// when nothing can be uploaded again. Errors name no part of the image.
func parseImageDataURI(uri string) (mime string, data []byte, err error) {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return "", nil, invalid("image is not a data: URI")
	}
	header, encoded, ok := strings.Cut(rest, ",")
	if !ok {
		return "", nil, invalid("image data: URI has no payload")
	}
	mime, ok = strings.CutSuffix(header, ";base64")
	if !ok {
		return "", nil, invalid("image data: URI is not base64")
	}
	mime = strings.ToLower(mime)
	if _, ok := imageExtensions[mime]; !ok {
		return "", nil, invalid("image must be image/jpeg or image/png")
	}
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxImageBytes+3 {
		return "", nil, invalid("image is larger than 10 MB")
	}
	data, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, invalid("image is not valid base64")
	}
	if len(data) == 0 {
		return "", nil, invalid("image is empty")
	}
	if len(data) > maxImageBytes {
		return "", nil, invalid("image is larger than 10 MB")
	}
	if detected := http.DetectContentType(data); detected != mime {
		return "", nil, invalid("image bytes are not the declared " + mime)
	}
	return mime, data, nil
}

// invalid is a pre-call refusal of the user's own data: the service answers
// it with a 422. reason names a field or a rule, never a value.
func invalid(reason string) error {
	return fmt.Errorf("blindpay: %s: %w", reason, ramp.ErrKycDataInvalid)
}
