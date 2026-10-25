// Package certs creates a local CA and TLS certificates for https://localhost.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"lnch.sh/internal/state"
)

// Paths of the local CA.
func Paths() (certPath, keyPath string, err error) {
	home, err := state.Home()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(home, "ca")
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"), nil
}

// Leaf returns a certificate for localhost signed by the lnch CA.
func Leaf() (tls.Certificate, error) {
	ca, caKey, caDER, err := loadCA()
	if err != nil {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "localhost", Organization: []string{"lnch"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 90),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der, caDER},
		PrivateKey:  key,
	}, nil
}

// Trust installs the local CA for the current user.
func Trust() (string, error) {
	certPath, _, err := loadOrCreate()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("certutil", "-addstore", "-user", "-f", "Root", certPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return certPath, fmt.Errorf("certutil failed: %w\n%s", err, out)
		}
		return certPath, nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return certPath, err
		}
		keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		cmd := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, certPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return certPath, fmt.Errorf("security add-trusted-cert failed: %w\n%s\nExample: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s", err, out, certPath)
		}
		return certPath, nil
	default:
		return certPath, fmt.Errorf("install the CA manually, then restart the browser\nsudo cp %s /usr/local/share/ca-certificates/lnch-local.crt\nsudo update-ca-certificates", certPath)
	}
}

func loadCA() (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	certPath, keyPath, err := loadOrCreate()
	if err != nil {
		return nil, nil, nil, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, nil, fmt.Errorf("lnch CA files in %s are unreadable", filepath.Dir(certPath))
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, nil, fmt.Errorf("lnch CA key is not an ECDSA key")
	}
	return cert, key, cb.Bytes, nil
}

var caMu sync.Mutex

func loadOrCreate() (string, string, error) {
	caMu.Lock()
	defer caMu.Unlock()
	certPath, keyPath, err := Paths()
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return certPath, keyPath, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return "", "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{Organization: []string{"lnch"}, CommonName: "lnch local CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}
