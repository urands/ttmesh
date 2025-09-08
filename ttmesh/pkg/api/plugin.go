package api

// Plugin is a marker for pluggable components (executors, transports, codecs, etc.).
// Concrete plugin registries should be defined in their respective packages.
type Plugin interface{ Name() string }
