package patterns

// GetKeyPatterns returns all private key detection patterns
func GetKeyPatterns() []*Pattern {
	return []*Pattern{
		rsaPrivateKeyPattern(),
		ecPrivateKeyPattern(),
		dsaPrivateKeyPattern(),
		opensshPrivateKeyPattern(),
		pgpPrivateKeyPattern(),
		pkcs8PrivateKeyPattern(),
		genericPrivateKeyPattern(),
	}
}

// RSA Private Key pattern
func rsaPrivateKeyPattern() *Pattern {
	return NewPattern("rsa_private_key").
		WithRegex(`-----BEGIN RSA PRIVATE KEY-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("RSA private key (PEM format)").
		Build()
}

// EC Private Key pattern
func ecPrivateKeyPattern() *Pattern {
	return NewPattern("ec_private_key").
		WithRegex(`-----BEGIN EC PRIVATE KEY-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("Elliptic Curve private key (PEM format)").
		Build()
}

// DSA Private Key pattern
func dsaPrivateKeyPattern() *Pattern {
	return NewPattern("dsa_private_key").
		WithRegex(`-----BEGIN DSA PRIVATE KEY-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("DSA private key (PEM format)").
		Build()
}

// OpenSSH Private Key pattern
func opensshPrivateKeyPattern() *Pattern {
	return NewPattern("openssh_private_key").
		WithRegex(`-----BEGIN OPENSSH PRIVATE KEY-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("OpenSSH private key").
		Build()
}

// PGP Private Key pattern
func pgpPrivateKeyPattern() *Pattern {
	return NewPattern("pgp_private_key").
		WithRegex(`-----BEGIN PGP PRIVATE KEY BLOCK-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("PGP/GPG private key block").
		Build()
}

// PKCS8 Private Key pattern (generic and encrypted)
func pkcs8PrivateKeyPattern() *Pattern {
	return NewPattern("pkcs8_private_key").
		WithRegex(`-----BEGIN (?:ENCRYPTED )?PRIVATE KEY-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("PKCS#8 private key (PEM format)").
		Build()
}

// Generic Private Key pattern - catches all private key types
func genericPrivateKeyPattern() *Pattern {
	return NewPattern("private_key").
		WithRegex(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY(?: BLOCK)?-----`).
		WithCategory(CategoryPrivateKey).
		WithSeverity(SeverityCritical).
		WithDescription("Private key (any format)").
		Build()
}
