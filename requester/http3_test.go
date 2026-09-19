// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package requester

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

// --- self-signed TLS for localhost ---

var (
	caCert      *x509.Certificate
	caPrivKey   ed25519.PrivateKey
	leafCert    *x509.Certificate
	leafPrivKey ed25519.PrivateKey
)

func initHTTPTLS(t *testing.T) {
	if caCert != nil {
		return
	}
	t.Helper()

	// CA key pair
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	caTempl := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject:      pkix.Name{},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	caBytes, err := x509.CreateCertificate(rand.Reader, caTempl, caTempl, caPub, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, _ = x509.ParseCertificate(caBytes)
	caPrivKey = caKey

	// Leaf key pair
	leafPub, leafKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf: %v", err)
	}
	leafTempl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	leafBytes, err := x509.CreateCertificate(rand.Reader, leafTempl, caCert, leafPub, caPrivKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	leafCert, _ = x509.ParseCertificate(leafBytes)
	leafPrivKey = leafKey
}

// http3ServerTLS returns a *tls.Config for an HTTP/3 server.
func http3ServerTLS() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafCert.Raw},
			PrivateKey:  leafPrivKey,
		}},
	}
}

// startHTTP3Server starts an HTTP/3 server on a random UDP port.
func startHTTP3Server(t *testing.T, handler http.Handler) (url string, srv *http3.Server, cancel func()) {
	t.Helper()
	initHTTPTLS(t)

	srv = &http3.Server{
		Handler:   handler,
		TLSConfig: http3ServerTLS(),
	}

	udpAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}

	go func() {
		_ = srv.Serve(conn)
	}()

	addr := conn.LocalAddr().(*net.UDPAddr)
	portStr := strconv.Itoa(addr.Port)
	url = "https://localhost:" + portStr
	cancel = func() {
		srv.Close()
		conn.Close()
	}
	return
}

func TestHTTP3Request(t *testing.T) {
	url, srv, cancel := startHTTP3Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("h3-ok"))
	}))
	defer cancel()
	_ = srv // used to keep the server alive

	req, _ := http.NewRequest("GET", url, nil)
	w := &Work{
		Request:   req,
		N:         1,
		C:         1,
		H3:        true,
		Timeout:   5,
	}
	w.Run()
}

func TestHTTP3Body(t *testing.T) {
	var gotBody string
	url, srv, cancel := startHTTP3Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer cancel()
	_ = srv

	req, _ := http.NewRequest("POST", url, nil)
	w := &Work{
		Request:     req,
		RequestBody: []byte("hello-h3"),
		N:           5,
		C:           1,
		H3:          true,
		Timeout:     5,
	}
	w.Run()
	if gotBody != "hello-h3" {
		t.Errorf("body = %q, want %q", gotBody, "hello-h3")
	}
}

func TestHTTP3Concurrency(t *testing.T) {
	var count int64
	url, srv, cancel := startHTTP3Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer cancel()
	_ = srv

	req, _ := http.NewRequest("GET", url, nil)
	w := &Work{
		Request: req,
		N:       50,
		C:       10,
		H3:      true,
		Timeout: 10,
	}
	w.Run()
	if count != 50 {
		t.Errorf("expected 50 requests, got %d", count)
	}
}

func TestHTTP3DisabledCompression(t *testing.T) {
	var gotAcceptEncoding string
	url, srv, cancel := startHTTP3Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer cancel()
	_ = srv

	req, _ := http.NewRequest("GET", url, nil)
	w := &Work{
		Request:            req,
		N:                  1,
		C:                  1,
		H3:                 true,
		Timeout:            5,
		DisableCompression: true,
	}
	w.Run()
	if gotAcceptEncoding != "" {
		t.Errorf("expected no Accept-Encoding, got %q", gotAcceptEncoding)
	}
}

func TestHTTP3PostWithBody(t *testing.T) {
	var bodyReceived []byte
	url, srv, cancel := startHTTP3Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyReceived, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}))
	defer cancel()
	_ = srv

	req, _ := http.NewRequest("POST", url, nil)
	w := &Work{
		Request:     req,
		RequestBody: []byte("test-payload"),
		N:           2,
		C:           1,
		H3:          true,
		Timeout:     5,
	}
	w.Run()
	if string(bodyReceived) != "test-payload" {
		t.Errorf("body = %q, want %q", bodyReceived, "test-payload")
	}
}
