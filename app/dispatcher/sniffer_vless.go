//go:build yue_profile_vless && !yue_profile_hy2

package dispatcher

// Yue's VLESS role does not configure inbound sniffing. Keep the shared TCP/UTP
// code source-compatible while physically excluding the QUIC parser and its
// quic-go dependency from the artifact.
func roleProtocolSniffers() []protocolSnifferWithMetadata { return nil }
