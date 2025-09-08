package config

// TransportConfig describes one transport kind and its endpoints.
// Example YAML:
// transports:
//   - kind: udp
//     listen: [":7777"]
//     dial:
//       - address: "10.0.0.2:7777"
//         peer_id: "worker-1"
//   - kind: quic
//     listen: [":4433"]
//     dial:
//       - address: "10.0.0.2:4433"
//   - kind: winpipe
//     listen: ["\\\\.\\pipe\\ttmesh"]
//   - kind: mem
//     listen: ["inproc://test"]
type TransportConfig struct {
    Kind   string            `mapstructure:"kind"`
    Listen []string          `mapstructure:"listen"`
    Dial   []PeerDialConfig  `mapstructure:"dial"`
    // Extra holds transport-specific options (reserved for future use)
    Extra  map[string]any    `mapstructure:"extra"`
}

// PeerDialConfig describes a target to dial on startup.
type PeerDialConfig struct {
    Address string `mapstructure:"address"`
    PeerID  string `mapstructure:"peer_id"`
}
