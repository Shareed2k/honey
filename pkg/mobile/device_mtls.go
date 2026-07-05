package mobile

import "github.com/shareed2k/honey/internal/devmtls"

// MTLSSigner is implemented on the mobile side (Android Keystore). Sign returns
// an ASN.1 DER ECDSA signature over the given already-hashed digest. The private
// key stays in the device TEE; only signatures cross the boundary.
type MTLSSigner interface {
	Sign(digest []byte) ([]byte, error)
}

// SetDeviceMTLS registers the device client-certificate chain (PEM), the gateway
// server CA (PEM; empty falls back to system roots), and the signer callback.
// After this, honey backends marked mtls are reached over the device client cert
// by the in-process engine (search, backend listing, and WS exec/tunnel).
func SetDeviceMTLS(chainPEM, caPEM string, signer MTLSSigner) {
	devmtls.Set([]byte(chainPEM), []byte(caPEM), signer)
}

// ClearDeviceMTLS removes the registered device mTLS credential (e.g. on logout).
func ClearDeviceMTLS() {
	devmtls.Clear()
}
