package core

// ConfigError is raised for any malformed configuration. It is always fatal at
// startup.
type ConfigError struct {
	Msg string
}

func (e *ConfigError) Error() string { return e.Msg }
