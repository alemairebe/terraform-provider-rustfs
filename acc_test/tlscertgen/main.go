// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

// Command tlscertgen writes the ephemeral TLS material the acceptance fixture
// serves. It runs once before rustfs-tls starts, so no private key is committed
// to the repository.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: tlscertgen <output-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]

	// The startup pair covers the service name and loopback. The renewed pair
	// must cover every startup name because RustFS rejects a publication that
	// drops a subject alternative name.
	startup := pair{
		name: "rustfs",
		dns:  []string{"rustfs-tls", "localhost"},
		ips:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	renewed := pair{
		name: "renewed",
		dns:  []string{"rustfs-tls", "localhost", "renewed.test"},
		ips:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	for _, p := range []pair{startup, renewed} {
		if err := p.write(dir); err != nil {
			fmt.Fprintf(os.Stderr, "tlscertgen: %v\n", err)
			os.Exit(1)
		}
	}
}

type pair struct {
	name string
	dns  []string
	ips  []net.IP
}

func (p pair) write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate %s key: %w", p.name, err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate %s serial: %w", p.name, err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "rustfs-tls"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              p.dns,
		IPAddresses:           p.ips,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create %s certificate: %w", p.name, err)
	}

	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	// The material is ephemeral test data shared between the generator and
	// the test process, which may run as a different (host) user.
	if err := os.WriteFile(filepath.Join(dir, p.name+"_cert.pem"), certificate, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, p.name+"_key.pem"), privateKey, 0o644)
}
