// Package pixrules holds the validation rules shared by an off-ramp request
// (POST /offramps, POST /offramps/estimate) and a saved Pix key (POST
// /pix-keys, PUT /pix-keys/{id}): the BACEN key-type vocabulary and the
// recipient-document requirement. A saved key that the off-ramp create would
// reject is useless; one looser than the off-ramp create lets a user save a
// key the withdraw flow later refuses. It also holds the key rule of each,
// which differ on purpose and in one direction only (see OfframpKeyRule).
package pixrules

// KeyTypeRule is the BACEN key-type set.
const KeyTypeRule = "required|in:cpf,cnpj,email,phone,evp"

// RecipientDocumentRule requires a document for phone, email, and evp keys;
// optional for cpf/cnpj, since the document is inferable from the key itself.
// It is one word: digits, or an alphanumeric CNPJ's shape (spec §19.1,
// rules.RecipientDocumentRule); every reader canonicalizes it
// (brazil.CanonicalDocument).
const RecipientDocumentRule = "required_if:pix_key_type,phone|required_if:pix_key_type,email|required_if:pix_key_type,evp|max_len:18|recipient_document"

// KeyRule is a saved key: valid for its pix_key_type and in the spelling the
// DICT stores, which is what both apps read back (rules.PixKeyCanonicalRule).
const KeyRule = "required|max_len:140|pix_key_canonical"

// OfframpKeyRule is the off-ramp's key: valid for its pix_key_type however it
// was typed (rules.PixKeyTypedRule). It is looser than KeyRule on purpose and
// only in that direction — every saved key passes it — because withdrawals
// that work today send a key spelled other ways (issue #73).
const OfframpKeyRule = "required|max_len:140|pix_key_typed"
