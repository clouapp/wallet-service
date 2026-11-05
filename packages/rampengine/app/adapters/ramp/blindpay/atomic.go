package blindpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

var _ ramp.AtomicKycCreator = (*Adapter)(nil)

// atomicVersion is the version of both the checkpoint and the prepared body.
// A row written under another version is refused rather than reinterpreted.
const atomicVersion = 1

// uploadBucket is BlindPay's bucket for customer onboarding files.
const uploadBucket = "onboarding"

// ValidateDeferredApplication checks everything the create will need from the
// application, without calling BlindPay, and answers the state the row waits
// in until the documents arrive: terms accepted, government id and selfie
// still owed.
func (*Adapter) ValidateDeferredApplication(app ramp.Application) (*ramp.KycState, error) {
	if err := validateApplication(app); err != nil {
		return nil, err
	}
	return &ramp.KycState{
		Status:            models.KycStatusIncomplete,
		TosStatus:         models.TosStatusApproved,
		SignedAgreementID: app.SignedAgreementID,
		Requirements:      []string{"government_id", "selfie"},
		Endorsements: []ramp.Endorsement{
			{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusIncomplete},
			{Name: models.EndorsementPix, Status: models.EndorsementStatusIncomplete},
		},
	}, nil
}

// ValidateAtomicDocuments checks the images and the client IP before anything
// is staged.
func (*Adapter) ValidateAtomicDocuments(docs ramp.Documents) error {
	_, err := decodeAtomicDocuments(docs)
	return err
}

func validateApplication(app ramp.Application) error {
	if app.Type != "" && app.Type != "individual" {
		return invalid("only individual customers are supported")
	}
	p := app.Profile
	required := []struct{ field, value string }{
		{"first_name", p.FirstName},
		{"last_name", p.LastName},
		{"email", p.Email},
		{"birth_date", p.BirthDate},
		{"phone", p.Phone},
		{"address.street_line_1", p.Address.StreetLine1},
		{"address.city", p.Address.City},
		{"address.postal_code", p.Address.PostalCode},
		{"address.country", p.Address.Country},
		{"signed_agreement_id", app.SignedAgreementID},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return invalid(r.field + " is required")
		}
	}
	if !validBirthDate(strings.TrimSpace(p.BirthDate)) {
		return invalid("birth_date must be YYYY-MM-DD")
	}
	if _, _, err := ramp.SplitPhone(p.Phone); err != nil {
		return invalid("phone must be E.164")
	}
	country, ok := iso3166.Alpha2(p.Address.Country)
	if !ok {
		return invalid("address.country is not a known country")
	}
	if _, err := stateProvinceRegion(p.Address.Subdivision, country); err != nil {
		return err
	}
	if taxID(app) == "" {
		return invalid("tax_id is required")
	}
	return nil
}

// decodeAtomicDocuments checks and decodes one identity document (front, and
// back when sent), one selfie and the client IP.
func decodeAtomicDocuments(docs ramp.Documents) (atomicImages, error) {
	var out atomicImages
	var withImages []ramp.IdentifyingDocument
	for _, doc := range docs.IdentifyingInformation {
		if doc.ImageFront == "" && doc.ImageBack != "" {
			return out, invalid("document back sent without its front")
		}
		if doc.ImageFront != "" {
			withImages = append(withImages, doc)
		}
	}
	if len(withImages) != 1 {
		return out, invalid("exactly one identity document with a front image is required")
	}
	out.document = withImages[0]
	if _, ok := idDocTypes[out.document.Type]; !ok {
		return out, invalid("document type must be passport, national_id or drivers_license")
	}
	if _, ok := iso3166.Alpha2(out.document.IssuingCountry); !ok {
		return out, invalid("document issuing country is not a known country")
	}
	var err error
	if out.front, err = decodeImage(out.document.ImageFront); err != nil {
		return out, err
	}
	if out.document.ImageBack != "" {
		back, err := decodeImage(out.document.ImageBack)
		if err != nil {
			return out, err
		}
		out.back = &back
	}
	if len(docs.LivenessCheckSelfies) != 1 || docs.LivenessCheckSelfies[0].Image == "" {
		return out, invalid("exactly one selfie is required")
	}
	if out.selfie, err = decodeImage(docs.LivenessCheckSelfies[0].Image); err != nil {
		return out, err
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(docs.ClientIPAddress))
	if err != nil || addr.IsUnspecified() {
		return out, invalid("client IP address is missing or invalid")
	}
	out.ipAddress = addr.Unmap().String()
	return out, nil
}

func decodeImage(uri string) (decodedImage, error) {
	mime, data, err := parseImageDataURI(uri)
	return decodedImage{mime: mime, data: data}, err
}

// PrepareCustomer uploads the files the create names and builds its exact
// body. BlindPay's upload has no idempotency, so each upload is followed by
// persist before the next one starts: a run that dies in between resumes from
// the checkpoint and reuses what it already uploaded, instead of orphaning
// one file per attempt.
func (a *Adapter) PrepareCustomer(ctx context.Context, sub ramp.AtomicKycSubmission, checkpoint ramp.AtomicKycCheckpoint,
	persist func(ramp.AtomicKycCheckpoint) error,
) (*ramp.PreparedAtomicKyc, error) {
	if err := validateApplication(sub.Application); err != nil {
		return nil, err
	}
	images, err := decodeAtomicDocuments(sub.Documents)
	if err != nil {
		return nil, err
	}
	if sub.SubmissionID == "" {
		return nil, fmt.Errorf("blindpay: submission without an id: %w", ramp.ErrAtomicMaterialInvalid)
	}
	checkpoint, err = startCheckpoint(checkpoint, sub.SubmissionID)
	if err != nil {
		return nil, err
	}

	files := []uploadFile{{fieldIDDocFront, images.front}}
	if images.back != nil {
		files = append(files, uploadFile{fieldIDDocBack, *images.back})
	}
	files = append(files, uploadFile{fieldSelfie, images.selfie})

	var client *Client
	handles := make(map[string]string, len(files))
	for _, file := range files {
		digest := fileDigest(sub.SubmissionID, file.field, file.image)
		if handle, ok := reusableHandle(checkpoint, file.field, digest); ok {
			handles[file.field] = handle
			continue
		}
		if client == nil {
			if client, err = a.client(ctx); err != nil {
				return nil, err
			}
		}
		filename := file.field + "." + imageExtensions[file.image.mime]
		handle, err := client.Upload(ctx, uploadBucket, filename, file.image.mime, file.image.data)
		if err != nil {
			return nil, err
		}
		checkpoint = recordUpload(checkpoint, ramp.AtomicKycUploadedField{
			Field: file.field, DocumentSHA256: digest, ProviderHandle: handle,
		})
		if err := persist(checkpoint); err != nil {
			return nil, fmt.Errorf("blindpay: persist upload checkpoint: %w", err)
		}
		handles[file.field] = handle
	}

	body, err := buildCustomerCreateRequest(sub, images, handles)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("blindpay: marshal customer: %w: %w", ramp.ErrRequestNotSent, err)
	}
	sum := sha256.Sum256(payload)
	return &ramp.PreparedAtomicKyc{
		Version:       atomicVersion,
		Payload:       payload,
		PayloadSHA256: hex.EncodeToString(sum[:]),
	}, nil
}

// uploadFile is one payload field and the image that fills it.
type uploadFile struct {
	field string
	image decodedImage
}

// startCheckpoint accepts an empty checkpoint (a first run) or one written by
// this submission under this version; anything else would reuse another
// submission's files.
func startCheckpoint(cp ramp.AtomicKycCheckpoint, submissionID string) (ramp.AtomicKycCheckpoint, error) {
	if cp.Version == 0 && cp.SubmissionID == "" && len(cp.Fields) == 0 {
		return ramp.AtomicKycCheckpoint{Version: atomicVersion, SubmissionID: submissionID}, nil
	}
	if cp.Version != atomicVersion {
		return cp, fmt.Errorf("blindpay: checkpoint version %d: %w", cp.Version, ramp.ErrAtomicMaterialInvalid)
	}
	if cp.SubmissionID != submissionID {
		return cp, fmt.Errorf("blindpay: checkpoint belongs to another submission: %w", ramp.ErrAtomicMaterialInvalid)
	}
	// Copied, so recording an upload never writes into the caller's slice.
	cp.Fields = append([]ramp.AtomicKycUploadedField(nil), cp.Fields...)
	return cp, nil
}

// fileDigest binds a file to its submission, field and type, so a handle is
// reused only for the very file it was uploaded for.
func fileDigest(submissionID, field string, image decodedImage) string {
	h := sha256.New()
	h.Write([]byte(submissionID + "\x00" + field + "\x00" + image.mime + "\x00"))
	h.Write(image.data)
	return hex.EncodeToString(h.Sum(nil))
}

func reusableHandle(cp ramp.AtomicKycCheckpoint, field, digest string) (string, bool) {
	for _, f := range cp.Fields {
		if f.Field == field && f.ProviderHandle != "" && hmac.Equal([]byte(f.DocumentSHA256), []byte(digest)) {
			return f.ProviderHandle, true
		}
	}
	return "", false
}

// recordUpload replaces the field's entry (a file that changed is uploaded
// again) or appends one.
func recordUpload(cp ramp.AtomicKycCheckpoint, uploaded ramp.AtomicKycUploadedField) ramp.AtomicKycCheckpoint {
	for i, f := range cp.Fields {
		if f.Field == uploaded.Field {
			cp.Fields[i] = uploaded
			return cp
		}
	}
	cp.Fields = append(cp.Fields, uploaded)
	return cp
}

// refusedCreate names a create BlindPay refused over what it carried — a 400
// or 422 no stable code explains — as ErrKycDataInvalid: the provider looked
// at the person and created no one. Every other failure is left as it is,
// because it proves nothing about whether a customer exists.
func refusedCreate(err error) error {
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.sentinel != nil {
		return err
	}
	// These two say a customer may already exist — possibly one an earlier
	// create of ours made and never heard back from. That is no refusal: the
	// outcome is unknown, and the error is left for the caller to hold.
	if customerMayExist[upstream.Code] {
		return err
	}
	if upstream.Status != http.StatusBadRequest && upstream.Status != http.StatusUnprocessableEntity {
		return err
	}
	return fmt.Errorf("%w: %w", ramp.ErrKycDataInvalid, err)
}

// customerMayExist are the create rejections that answer about a customer
// already there. The OpenAPI document lists both under the create's 400 and
// the error table under 409; either way they are not a refusal of the data.
var customerMayExist = map[string]bool{
	"TOS_ALREADY_ACCEPTED":       true,
	"CUSTOMERS_ALREADY_APPROVED": true,
}

type customerCreateResponse struct {
	ID         string `json:"id"`
	CustomerID string `json:"customer_id"`
}

// CreatePreparedCustomer sends the prepared bytes as they are. A create that
// succeeded and a read of the new customer that failed answer the id AND the
// error, so the service links the customer before reporting the failure.
func (a *Adapter) CreatePreparedCustomer(ctx context.Context, idempotencyKey string, prepared ramp.PreparedAtomicKyc) (*ramp.KycState, error) {
	if prepared.Version != atomicVersion {
		return nil, fmt.Errorf("blindpay: prepared customer version %d: %w", prepared.Version, ramp.ErrAtomicMaterialInvalid)
	}
	sum := sha256.Sum256(prepared.Payload)
	if len(prepared.Payload) == 0 || !hmac.Equal([]byte(hex.EncodeToString(sum[:])), []byte(prepared.PayloadSHA256)) {
		return nil, fmt.Errorf("blindpay: prepared customer digest does not match its body: %w", ramp.ErrAtomicMaterialInvalid)
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("blindpay: customer create without an idempotency key: %w", ramp.ErrAtomicMaterialInvalid)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var created customerCreateResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "customers", RawBody: prepared.Payload, IdempotencyKey: idempotencyKey,
	}, &created); err != nil {
		return nil, refusedCreate(err)
	}
	if strings.TrimSpace(created.ID) == "" || created.ID != created.CustomerID {
		return nil, fmt.Errorf("blindpay: customer create answered no usable id: %w", ramp.ErrProviderFailed)
	}
	customer, raw, err := a.fetchCustomer(ctx, client, created.ID)
	if err != nil {
		return &ramp.KycState{ExternalCustomerID: created.ID}, err
	}
	return kycStateFrom(customer, raw), nil
}
