package config

// IdentityConfig describes cryptographic identity settings.
type IdentityConfig struct {
    Alg            string `mapstructure:"alg"`               // e.g., ed25519
    PrivateKey     string `mapstructure:"private_key"`       // base64url(no padding) of raw private key bytes
    PrivateKeyFile string `mapstructure:"private_key_file"`  // path to file containing base64 or raw bytes
}

