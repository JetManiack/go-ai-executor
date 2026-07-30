package config

// TODO: Implement LoadConfig
func LoadConfig(ctx context.Context, flags map[string]string, yamlPath string) (Config, error) {
	return Config{}, nil
}

type Config struct {
	ListenAddr   string
	DbDsn        string
	SandboxDir   string
	Transport    string
	AuthToken    string
	DefaultTimeout int
	MaxOutputKB   int
	Shell        string
	Devel        bool
}
