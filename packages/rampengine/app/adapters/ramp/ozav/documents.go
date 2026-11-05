package ozav

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// documentMimes is what Ozav accepts; anything else is refused before the
// declare, leaving nothing behind.
var documentMimes = map[string]struct{}{
	"application/pdf": {},
	"image/png":       {},
	"image/jpeg":      {},
	"image/webp":      {},
	"image/heic":      {},
}

// Ozav's document vocabulary, narrowed to what XIP collects. A selfie is
// declared `selfie`, not `liveness`: Ozav's liveness is a capture session
// proving presence, and XIP holds a still image. A proof of address and a
// bank statement are single-sided and have no issuing country.
const (
	docTypeIdentity       = "identity_document"
	docTypeSelfie         = "selfie"
	docTypeProofOfAddress = "proof_of_address"
	docTypeBankStatement  = "bank_statement"

	sideFront  = "front"
	sideBack   = "back"
	sideSingle = "single"
)

// documentDeclareRequest is the body of POST /customers/{id}/documents. The
// optional integrity hints are sent: the digest is the only way to learn that
// what Ozav stored is what XIP sent.
type documentDeclareRequest struct {
	DocType        string `json:"doc_type"`
	Side           string `json:"side,omitempty"`
	IssuingCountry string `json:"issuing_country,omitempty"`
	Mime           string `json:"mime,omitempty"`
	SizeBytes      int    `json:"size_bytes,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
}

// documentResponse answers both the declare (with the upload grant) and the
// PUT that confirms receipt.
type documentResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	SHA256    string `json:"sha256"`
	UploadURL string `json:"upload_url"`
}

// pendingUpload is one decoded image. The bytes are PII and never reach a log.
type pendingUpload struct {
	docType        string
	side           string
	issuingCountry string
	mime           string
	sha256         string
	bytes          []byte
}

// SubmitDocuments uploads the user's identity images to an existing customer,
// decoding the whole batch before the first call: a declare Ozav accepts with
// no bytes after it stays `pending_upload` forever, and there is no endpoint
// to delete it.
func (a *Adapter) SubmitDocuments(ctx context.Context, ref ramp.CustomerRef, docs ramp.Documents) (*ramp.KycState, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}

	uploads, err := uploadsFrom(docs)
	if err != nil {
		return nil, err
	}
	if len(uploads) == 0 {
		return nil, errors.New("ozav: submit documents: no document images supplied")
	}

	for _, upload := range uploads {
		if err := a.uploadDocument(ctx, ref.ExternalID, upload, docs.IdempotencyKey); err != nil {
			return nil, err
		}
	}

	// The uploads say nothing about compliance; the customer's own answer does.
	return a.GetKyc(ctx, ref)
}

// uploadDocument runs the two steps for one image.
func (a *Adapter) uploadDocument(ctx context.Context, customerID string, upload pendingUpload, idempotencyKey string) error {
	// Resolved once: the declare and the PUT must reach the same Ozav with the
	// same key.
	client, err := a.client(ctx)
	if err != nil {
		return err
	}

	var declared documentResponse
	err = client.Do(ctx, Request{
		Method: http.MethodPost,
		Path:   customerPath(customerID) + "/documents",
		Body: documentDeclareRequest{
			DocType:        upload.docType,
			Side:           upload.side,
			IssuingCountry: upload.issuingCountry,
			Mime:           upload.mime,
			SizeBytes:      len(upload.bytes),
			SHA256:         upload.sha256,
		},
		// Each document gets its own key, derived from the submission's and
		// the document's identity. Reusing the submission's key across the
		// batch would make the second declare a replay of the first and Ozav
		// would answer with the first document.
		IdempotencyKey: documentIdempotencyKey(idempotencyKey, customerID, upload),
	}, &declared)
	if err != nil {
		return err
	}
	if declared.UploadURL == "" {
		return fmt.Errorf("ozav: document %s/%s: declare returned no upload_url", upload.docType, upload.side)
	}

	var received documentResponse
	if err := client.Upload(ctx, declared.UploadURL, upload.mime, upload.bytes, &received); err != nil {
		return err
	}

	// A truncated PUT still answers 200; Ozav's digest of what it received is
	// the only evidence that what is on file is what was sent.
	if !strings.EqualFold(received.SHA256, upload.sha256) {
		return fmt.Errorf("ozav: document %s/%s: sha256 mismatch: sent %s, provider stored %s",
			upload.docType, upload.side, upload.sha256, received.SHA256)
	}
	return nil
}

// documentIdempotencyKey is stable across a retry of the submission and
// distinct per document. The content digest is what separates them: doc_type
// and side are not unique (a national id and a passport are both
// identity_document/front), and a shared key would have Ozav answer the second
// declare with the first document's grant, a corruption the sha256 check
// cannot see. The client normalizes the separators.
func documentIdempotencyKey(submissionKey, customerID string, upload pendingUpload) string {
	// A prefix separates the documents and keeps the key under the ceiling.
	fingerprint := upload.sha256[:16]
	if submissionKey == "" {
		// No key from the service: the content alone keeps a retry stable.
		return fmt.Sprintf("ozav-doc-%s-%s-%s", customerID, upload.docType, fingerprint)
	}
	return fmt.Sprintf("%s-%s-%s", submissionKey, upload.docType, fingerprint)
}

// uploadsFrom decodes every image, refusing the whole batch if one is unusable.
// An entry with no images (the CPF, which reaches Ozav as the customer's
// tax_id) is skipped.
func uploadsFrom(docs ramp.Documents) ([]pendingUpload, error) {
	uploads := make([]pendingUpload, 0, len(docs.IdentifyingInformation)*2+len(docs.LivenessCheckSelfies)+2)

	for _, doc := range docs.IdentifyingInformation {
		country, err := optionalAlpha2(doc.IssuingCountry)
		if err != nil {
			return nil, err
		}
		for _, face := range []struct {
			image string
			side  string
		}{{doc.ImageFront, sideFront}, {doc.ImageBack, sideBack}} {
			if face.image == "" {
				continue
			}
			upload, err := uploadFrom(face.image, docTypeIdentity, face.side)
			if err != nil {
				return nil, err
			}
			upload.issuingCountry = country
			uploads = append(uploads, upload)
		}
	}

	for _, selfie := range docs.LivenessCheckSelfies {
		if selfie.Image == "" {
			continue
		}
		upload, err := uploadFrom(selfie.Image, docTypeSelfie, sideSingle)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}

	for _, single := range []struct{ file, docType string }{
		{docs.ProofOfAddress, docTypeProofOfAddress},
		{docs.BankStatement, docTypeBankStatement},
	} {
		if single.file == "" {
			continue
		}
		upload, err := uploadFrom(single.file, single.docType, sideSingle)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}

	return uploads, nil
}

func uploadFrom(image, docType, side string) (pendingUpload, error) {
	mime, payload, err := splitDataURI(image)
	if err != nil {
		return pendingUpload{}, err
	}
	// Checked before decoding, so an unsupported type is reported as one.
	if _, ok := documentMimes[mime]; !ok {
		return pendingUpload{}, fmt.Errorf("ozav: %w: %s is not a mime Ozav accepts for %s",
			ramp.ErrDocumentsNotSupported, mime, docType)
	}
	decoded, err := decodeDataURIPayload(payload)
	if err != nil {
		return pendingUpload{}, err
	}
	sum := sha256.Sum256(decoded)
	return pendingUpload{
		docType: docType,
		side:    side,
		mime:    mime,
		sha256:  hex.EncodeToString(sum[:]),
		bytes:   decoded,
	}, nil
}

// splitDataURI reads the "data:<mime>;base64," header off an image. No error
// here quotes the payload: it is a document image.
func splitDataURI(value string) (string, string, error) {
	const prefix = "data:"
	if !strings.HasPrefix(value, prefix) {
		return "", "", errors.New("ozav: document image is not a data URI")
	}
	header, payload, found := strings.Cut(value[len(prefix):], ",")
	if !found {
		return "", "", errors.New("ozav: document image data URI has no payload")
	}
	mime, encoding, _ := strings.Cut(header, ";")
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return "", "", errors.New("ozav: document image data URI is not base64 encoded")
	}
	return strings.ToLower(strings.TrimSpace(mime)), payload, nil
}

func decodeDataURIPayload(payload string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return nil, errors.New("ozav: document image is not valid base64")
	}
	if len(decoded) == 0 {
		return nil, errors.New("ozav: document image is empty")
	}
	return decoded, nil
}
