package policies

// PermFeaturesUpdate and PermFeaturesAccountUpdate are the pair S2.4 names
// on PUT /v1/platform/features/{scope}/{id}[/{feature}].
// features.update covers any scope that route accepts.
// features.account.update covers the account scope only.
// There is no platform permission catalog and neither name is a permission
// row, so a platform_admins row stands in for both. The pair is not a
// second gate. No account role holds either name.
const (
	PermFeaturesUpdate        = "features.update"
	PermFeaturesAccountUpdate = "features.account.update"
)
