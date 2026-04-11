package config

// Boot loads all configuration into the Goravel config store. Called from bootstrap via WithConfig.
func Boot() {
	registerDatabase()
	registerCache()
	registerApp()
	registerAuth()
	registerJWT()
	registerMail()
	registerQueue()
	registerHTTP()
	registerVault()
	registerSecurity()
}
