package ramp

// ApplicationDocumentsRequirer is a provider that asks every customer for some
// supporting documents, and so wants them with the first application. Named
// only once the customer exists, they would cost the user a second pass
// through the wizard for something that was never in doubt.
//
// It is optional, like TosAttester: only Ozav asks for anything up front, and
// the assertion failing is the answer "nothing".
type ApplicationDocumentsRequirer interface {
	// ApplicationDocuments names them in the provider's own requirement
	// vocabulary — the words it uses for them once the customer exists — so
	// the KYC service turns them into wizard steps through the same mapping.
	ApplicationDocuments() []string
}

// ApplicationDocumentsOf is what adapter wants with a first application, nil
// for a provider that asks for nothing up front.
func ApplicationDocumentsOf(adapter ProviderAdapter) []string {
	requirer, ok := adapter.(ApplicationDocumentsRequirer)
	if !ok {
		return nil
	}
	return requirer.ApplicationDocuments()
}
