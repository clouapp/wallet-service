package config

// Boot loads all configuration into the Goravel config store. Called from bootstrap via WithConfig.
func Boot() {
	registerDatabase()
	registerCache()
	registerApp()
	registerAuth()
	registerJWT()
	registerHashing()
	registerMail()
	registerQueue()
	registerHTTP()
	registerVault()
	registerFeeEstimate()
}
