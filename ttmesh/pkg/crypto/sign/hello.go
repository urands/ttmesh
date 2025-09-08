package sign

import (
    "encoding/base64"
    "strconv"
    "strings"
)

// HelloTranscript builds the canonical transcript used for signing/verifying
// PeerHello messages. Format:
//   ttmesh:hello|v=1|alg=<alg>|ts=<unix_ms>|pub=<b64url>|nonce=<b64url>|name=<nodeName>
func HelloTranscript(alg string, pub, nonce []byte, tsUnixMS int64, nodeName string) []byte {
    b64 := base64.RawURLEncoding
    var sb strings.Builder
    sb.Grow(64 + len(nodeName))
    sb.WriteString("ttmesh:hello|v=1|alg=")
    sb.WriteString(strings.ToLower(strings.TrimSpace(alg)))
    sb.WriteString("|ts=")
    sb.WriteString(strconv.FormatInt(tsUnixMS, 10))
    sb.WriteString("|pub=")
    sb.WriteString(b64.EncodeToString(pub))
    sb.WriteString("|nonce=")
    sb.WriteString(b64.EncodeToString(nonce))
    sb.WriteString("|name=")
    sb.WriteString(nodeName)
    return []byte(sb.String())
}

