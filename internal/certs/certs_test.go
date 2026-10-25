package certs

import (
	"crypto/x509"
	"testing"
)

func TestLeafVerifies(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	leaf, err := Leaf()
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.Certificate) < 2 {
		t.Fatal("expected leaf and CA")
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(leaf.Certificate[1])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{
		DNSName:   "localhost",
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatal(err)
	}
}
