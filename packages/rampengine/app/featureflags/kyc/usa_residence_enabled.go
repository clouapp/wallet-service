package kyc

import "github.com/macrowallets/waas/packages/rampengine/app/featureflags"

// USAResidenceEnabled opens the United States as a KYC residence: listed by
// GET /api/kyc/countries, and served to an application or a terms request
// that declares it. Off by default in every environment: an operator opens it
// in the admin (local and staging too) once installed builds read an ssn for
// what it is (KYC USA spec R2). Off, every KYC route answers a US resident as
// for a residence nobody serves (jurisdiction_not_served), a retained profile
// included. A customer already approved keeps transacting. It holds people
// only: a company incorporated in the United States is served either way
// (KYB, PLANO B2).
const USAResidenceEnabled = "kyc-usa-residence-enabled"

var usaResidenceEnabled = featureflags.Definition{
	Name:  USAResidenceEnabled,
	Label: "KYC de residentes nos EUA",
	Description: "Abre os EUA no catálogo de países e no KYC (SSN ou ITIN, só saque Pix). " +
		"Desligada por padrão em todo ambiente: ligue aqui quando o app com SSN estiver nas lojas " +
		"(em local e staging, ligue aqui também). Desligada, toda rota de KYC recusa residente dos EUA " +
		"(jurisdiction_not_served), inclusive quem já tem perfil retido; quem já foi aprovado continua transacionando. " +
		"Empresas dos EUA (EIN) não dependem desta chave.",
	Scopes:  []featureflags.ScopeType{featureflags.ScopeGlobal},
	Default: false,
}
