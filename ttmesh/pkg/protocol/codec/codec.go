package codec

// Codec defines a simple interface for marshaling typed messages.
// Implementations should be deterministic and safe for cross-node exchange.
type Codec interface {
    ContentType() string
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
}

// Registry maps format/content type aliases to codecs.
type Registry struct { byType map[string]Codec }

// NewRegistry constructs a registry preloaded with built-in codecs
// that don't require initialization: JSON and Protobuf.
// CBOR can be added explicitly via Register(CBOR()).
func NewRegistry() *Registry {
    r := &Registry{byType: make(map[string]Codec)}
    // Built-ins without error paths
    r.Register(JSON())
    r.Register(Proto())
    return r
}

// Register adds a codec.
func (r *Registry) Register(c Codec) { r.byType[c.ContentType()] = c }

// Get returns a codec by content type, or nil.
func (r *Registry) Get(contentType string) Codec { return r.byType[contentType] }
