// Package devmtls holds the process-wide device mTLS client credential used to
// reach honey servers behind an mTLS gateway. The private key never lives here:
// signing is delegated to a registered Signer (on mobile, a callback into the
// Android Keystore over a gomobile reverse binding), so the key stays in the TEE
// while Go performs the TLS handshake.
package devmtls

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Signer signs a (already-hashed) digest with the device private key, returning
// an ASN.1 DER ECDSA signature. Implemented off-process (e.g. Android Keystore
// "NONEwithECDSA"); the key is never exported.
type Signer interface {
	Sign(digest []byte) ([]byte, error)
}

type material struct {
	chainPEM []byte
	caPEM    []byte
	signer   Signer
}

var (
	mu  sync.RWMutex
	cur *material
)

// Set registers the device client-cert chain (PEM), the default gateway server
// CA (PEM, may be empty), and the signer. Replaces any prior registration.
func Set(chainPEM, caPEM []byte, s Signer) {
	mu.Lock()
	defer mu.Unlock()
	cur = &material{chainPEM: chainPEM, caPEM: caPEM, signer: s}
}

// Clear removes the registration (e.g. on logout / re-enroll).
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	cur = nil
}

// Registered reports whether a usable device mTLS credential is available.
func Registered() bool {
	mu.RLock()
	defer mu.RUnlock()
	return cur != nil && cur.signer != nil && len(cur.chainPEM) > 0
}

// callbackSigner adapts a Signer into a crypto.Signer for tls.Certificate.
type callbackSigner struct {
	pub crypto.PublicKey
	s   Signer
}

func (c callbackSigner) Public() crypto.PublicKey { return c.pub }

func (c callbackSigner) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	return c.s.Sign(digest)
}

// ClientTLSConfig builds an mTLS client config from the registered credential.
// serverCAPEM, when non-empty, overrides the registered CA for trusting the
// gateway server certificate; empty falls back to the registered CA (else the
// system roots).
func ClientTLSConfig(serverCAPEM string) (*tls.Config, error) {
	mu.RLock()
	m := cur
	mu.RUnlock()
	if m == nil || m.signer == nil || len(m.chainPEM) == 0 {
		return nil, errors.New("device mTLS not registered")
	}

	var der [][]byte
	var leaf *x509.Certificate
	rest := m.chainPEM
	for {
		blk, r := pem.Decode(rest)
		if blk == nil {
			break
		}
		rest = r
		if blk.Type != "CERTIFICATE" {
			continue
		}
		der = append(der, blk.Bytes)
		if leaf == nil {
			c, err := x509.ParseCertificate(blk.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse client cert: %w", err)
			}
			leaf = c
		}
	}
	if leaf == nil {
		return nil, errors.New("no client certificate in chain")
	}

	cert := tls.Certificate{
		Certificate: der,
		PrivateKey:  callbackSigner{pub: leaf.PublicKey, s: m.signer},
		Leaf:        leaf,
	}

	caPEM := []byte(serverCAPEM)
	if len(bytes.TrimSpace(caPEM)) == 0 {
		caPEM = m.caPEM
	}
	var roots *x509.CertPool
	if len(bytes.TrimSpace(caPEM)) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("invalid server CA PEM")
		}
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots, // nil => system roots
		MinVersion:   tls.VersionTLS12,
	}, nil
}
