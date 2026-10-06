package secretfile

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/ssh"
)

func TestSniffFor(t *testing.T) {
	for _, c := range []struct {
		name string
		size int64
		want SniffKind
	}{
		{"cert.pem", 2 << 10, SniffDeep},
		{"Deck.key", 300 << 20, SniffDeep},
		{"CA.CRT", 1, SniffDeep},
		{"a.cer", 1, SniffDeep},
		{"a.cert", 1, SniffDeep},
		{"a.der", 1, SniffDeep},
		{"a.asc", 1, SniffDeep},
		{"a.gpg", 1, SniffDeep},
		{"a.pgp", 1, SniffDeep},
		{"a.sec", 1, SniffDeep},
		{"a.skr", 1, SniffDeep},
		{"server.key.bak", 5 << 20, SniffDeep},
		{"key (1).pem", 1 << 30, SniffDeep},
		{"privkey.pem~", 1, SniffDeep},
		{"cert.pem.1", 1, SniffDeep}, // wget's second download
		{"key.pem 2", 1 << 30, SniffDeep},
		{"shadow.5", 100, SniffText},
		{"notes.txt", 0, SniffText},
		{"notes.txt", TextLimit, SniffText},
		{"notes.txt", TextLimit + 1, SniffNone},
		{"photo.jpg", 10 << 10, SniffText},
		{"photo.jpg", 5 << 20, SniffNone},
		{"Makefile", 100, SniffText},
		{"pem", 100 << 10, SniffNone},
		{"archive.pem.zip", 1 << 20, SniffNone},
		{"id_ed25519.pub", 1 << 20, SniffNone},
	} {
		if got := SniffFor(c.name, c.size); got != c.want {
			t.Errorf("SniffFor(%q, %d) = %d, want %d", c.name, c.size, got, c.want)
		}
	}
}

type fixture struct {
	name   string
	data   []byte
	marker string // what Sniff finds; "" for nothing
}

func TestSniff(t *testing.T) {
	for _, c := range append(keyFixtures(t), literalFixtures()...) {
		kind := SniffFor(c.name, int64(len(c.data)))
		marker, found, err := Sniff(bytes.NewReader(c.data), int64(len(c.data)), c.name, kind)
		if err != nil || marker != c.marker || found != (c.marker != "") {
			t.Errorf("Sniff(%s, kind %d) = %q, %v, %v; want %q", c.name, kind, marker, found, err, c.marker)
		}
	}
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// widen encodes text as UTF-16 (size 2) or UTF-32 (size 4) in order, after a
// byte order mark if bom.
func widen(text []byte, size int, order binary.AppendByteOrder, bom bool) []byte {
	rs := []rune(string(text))
	if bom {
		rs = slices.Insert(rs, 0, 0xfeff)
	}
	var b []byte
	for _, r := range rs {
		if size == 4 {
			b = order.AppendUint32(b, uint32(r))
			continue
		}
		for _, u := range utf16.AppendRune(nil, r) {
			b = order.AppendUint16(b, u)
		}
	}
	return b
}

// utf16LE is what Windows PowerShell 5.1 writes by default.
func utf16LE(text []byte) []byte { return widen(text, 2, binary.LittleEndian, true) }

// keyFixtures are freshly generated keys and certificates.
func keyFixtures(t *testing.T) []fixture {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	check(err)
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	edPub, edk, err := ed25519.GenerateKey(rand.Reader)
	check(err)

	pkcs1 := x509.MarshalPKCS1PrivateKey(rk)
	sec1, err := x509.MarshalECPrivateKey(ek)
	check(err)
	pkcs8 := map[string][]byte{}
	for name, k := range map[string]crypto.PrivateKey{"rsa": rk, "ecdsa": ek, "ed25519": edk} {
		pkcs8[name], err = x509.MarshalPKCS8PrivateKey(k)
		check(err)
	}
	openssh := map[string][]byte{}
	for name, k := range map[string]crypto.PrivateKey{"rsa": rk, "ecdsa": ek, "ed25519": edk} {
		b, err := ssh.MarshalPrivateKey(k, "alice@laptop")
		check(err)
		openssh[name] = pem.EncodeToMemory(b)
	}
	encrypted, err := ssh.MarshalPrivateKeyWithPassphrase(edk, "", []byte("correct horse"))
	check(err)
	sshPub, err := ssh.NewPublicKey(edPub)
	check(err)

	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example CA"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true, IsCA: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &ek.PublicKey, ek)
	check(err)
	issuer, err := x509.ParseCertificate(certDER)
	check(err)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "files.example.com"},
		DNSNames: []string{"files.example.com"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, issuer, &rk.PublicKey, ek)
	check(err)
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, ek)
	check(err)
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: time.Now(), NextUpdate: time.Now().Add(time.Hour),
	}, issuer, ek)
	check(err)
	pubDER, err := x509.MarshalPKIXPublicKey(&ek.PublicKey)
	check(err)
	dhDER, err := asn1.Marshal(struct{ P, G *big.Int }{rk.N, big.NewInt(2)}) // DH parameters
	check(err)

	certPEM := pemBlock("CERTIFICATE", certDER)
	rsaPEM := pemBlock("RSA PRIVATE KEY", pkcs1)
	b64 := base64.StdEncoding.EncodeToString
	kubeconfig := func(user string) []byte {
		return []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: prod\n  cluster:\n    certificate-authority-data: " +
			b64(certPEM) + "\n    server: https://k8s.example.com:6443\nusers:\n- name: admin\n  user:\n" + user)
	}
	// The base64 of a PEM key folded at 76 columns, as in a YAML block scalar.
	var folded strings.Builder
	for s := b64(rsaPEM); s != ""; {
		n := min(76, len(s))
		folded.WriteString("    " + s[:n] + "\n")
		s = s[n:]
	}

	notes := append([]byte("# Schlüssel für prod, 鍵\r\n"), rsaPEM...) // not all ASCII
	der := ecKeyWithoutPublic(t)
	if bytes.IndexByte(der, 0) >= 0 {
		t.Fatal("EC key fixture has a NUL byte")
	}

	var deck bytes.Buffer
	zw := zip.NewWriter(&deck)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "Index/Slide.iwa", Method: zip.Store})
	check(err)
	w.Write(rsaPEM) // even a stored key inside a zip isn't looked at
	check(zw.Close())

	return []fixture{
		// PEM private keys.
		{"rsa.pem", rsaPEM, "private key"},
		{"key.pem", pemBlock("PRIVATE KEY", pkcs8["rsa"]), "private key"},
		{"ec.key", pemBlock("EC PRIVATE KEY", sec1), "private key"},
		{"ecdsa.pem", pemBlock("PRIVATE KEY", pkcs8["ecdsa"]), "private key"},
		{"ed25519.key", pemBlock("PRIVATE KEY", pkcs8["ed25519"]), "private key"},
		{"key.pem.bak", pemBlock("PRIVATE KEY", pkcs8["ed25519"]), "private key"},
		{"deploy_key", openssh["rsa"], "private key"},
		{"ecdsa.txt", openssh["ecdsa"], "private key"},
		{"notes.md", append([]byte("# Server notes\n\n"), openssh["ed25519"]...), "private key"},
		{"backup.key", pem.EncodeToMemory(encrypted), "private key"},
		{"haproxy.crt", append(pemBlock("CERTIFICATE", leafDER), rsaPEM...), "private key"},
		{"cluster.yaml", kubeconfig("    client-certificate-data: " + b64(pemBlock("CERTIFICATE", leafDER)) +
			"\n    client-key-data: " + b64(rsaPEM) + "\n"), "private key"},
		{"cluster-folded.yaml", kubeconfig("    client-key-data: |\n" + folded.String()), "private key"},
		{"tls-secret.yaml", []byte("apiVersion: v1\nkind: Secret\ntype: kubernetes.io/tls\ndata:\n  tls.crt: " +
			b64(certPEM) + "\n  tls.key: " + b64(pemBlock("PRIVATE KEY", pkcs8["ecdsa"])) + "\n"), "private key"},

		// DER private keys.
		{"server.key", pkcs1, "private key"},
		{"key.der", pkcs8["ed25519"], "private key"},
		{"ec.der", sec1, "private key"},
		{"nopub.key", der, "private key"}, // no NUL byte, still a key
		{"key.bin", pkcs1, ""},            // binary keys are only recognized by extension
		{"server.pem", pkcs1, "private key"},
		{"key.crt", pkcs8["rsa"], "private key"},
		{"ec.cer", sec1, "private key"},
		{"ed25519.cert", pkcs8["ed25519"], "private key"},
		{"nopub.pem", der, "private key"},

		// UTF-16 and UTF-32: Windows PowerShell 5.1 writes UTF-16 for
		// "terraform output -raw key > key.pem".
		{"utf16.pem", utf16LE(rsaPEM), "private key"},
		{"utf16.txt", utf16LE(pemBlock("PRIVATE KEY", pkcs8["ecdsa"])), "private key"},
		{"utf16be.key", widen(rsaPEM, 2, binary.BigEndian, true), "private key"},
		{"utf32.txt", widen(openssh["rsa"], 4, binary.LittleEndian, true), "private key"},
		{"utf32be.txt", widen(openssh["rsa"], 4, binary.BigEndian, true), "private key"},
		{"nobom.txt", widen(openssh["ed25519"], 2, binary.LittleEndian, false), "private key"},
		{"nobom-be.txt", widen(openssh["ed25519"], 2, binary.BigEndian, false), "private key"},
		{"nobom-notes.txt", widen(notes, 2, binary.LittleEndian, false), "private key"},
		{"utf16-cluster.yaml", utf16LE(kubeconfig("    client-key-data: " + b64(rsaPEM) + "\n")), "private key"},
		{"utf16-odd.txt", append(utf16LE(rsaPEM), 'x'), "private key"}, // a stray last byte
		{"utf16-cert.pem", utf16LE(certPEM), ""},
		{"utf32-cert.crt", widen(certPEM, 4, binary.LittleEndian, true), ""},
		{"nobom-cert.pem", widen(certPEM, 2, binary.LittleEndian, false), ""},
		{"utf16-pub.txt", utf16LE(ssh.MarshalAuthorizedKey(sshPub)), ""},

		// Public material.
		{"cert.pem", certPEM, ""},
		{"fullchain.pem", append(pemBlock("CERTIFICATE", leafDER), certPEM...), ""},
		{"ca.crt", certPEM, ""},
		{"ca.cer", certPEM, ""},
		{"ca.cert", certPEM, ""},
		{"cert.der", certDER, ""},
		{"cert.cer", leafDER, ""},
		{"cert.key", certDER, ""},
		{"cert-der.pem", certDER, ""}, // DER certificates start with a nested SEQUENCE
		{"leaf.crt", leafDER, ""},
		{"leaf.cert", leafDER, ""},
		{"csr-der.pem", csrDER, ""},
		{"crl.crt", crlDER, ""},
		{"pub.cer", pubDER, ""},
		{"rsa-pub.crt", x509.MarshalPKCS1PublicKey(&rk.PublicKey), ""},
		{"dh.pem", dhDER, ""},
		{"csr.pem", pemBlock("CERTIFICATE REQUEST", csrDER), ""},
		{"csr.der", csrDER, ""},
		{"crl.pem", pemBlock("X509 CRL", crlDER), ""},
		{"crl.der", crlDER, ""},
		{"pub.pem", pemBlock("PUBLIC KEY", pubDER), ""},
		{"pub.der", pubDER, ""},
		{"rsa-pub.pem", pemBlock("RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&rk.PublicKey)), ""},
		{"ecparam.pem", pemBlock("EC PARAMETERS", []byte{0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07}), ""},
		{"id_ed25519.pub", ssh.MarshalAuthorizedKey(sshPub), ""},
		{"cluster-certs.yaml", kubeconfig("    client-certificate-data: " + b64(pemBlock("CERTIFICATE", leafDER)) + "\n"), ""},
		{"Deck.key", deck.Bytes(), ""},
		{"Slides.key", append([]byte("PK\x03\x04"), rsaPEM...), ""}, // zips never match, whatever follows
	}
}

// ecKeyWithoutPublic returns a SEC 1 DER key without the optional public key
// (openssl ec -no_public) and without NUL bytes.
func ecKeyWithoutPublic(t *testing.T) []byte {
	t.Helper()
	for range 100 {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		sec1, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		var full struct {
			Version    int
			PrivateKey []byte
			Curve      asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
			PublicKey  asn1.BitString        `asn1:"optional,explicit,tag:1"`
		}
		if _, err := asn1.Unmarshal(sec1, &full); err != nil {
			t.Fatal(err)
		}
		der, err := asn1.Marshal(struct {
			Version    int
			PrivateKey []byte
			Curve      asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
		}{full.Version, full.PrivateKey, full.Curve})
		if err != nil {
			t.Fatal(err)
		}
		if bytes.IndexByte(der, 0) < 0 {
			return der
		}
	}
	t.Fatal("no EC key without NUL bytes")
	return nil
}

// literalFixtures are formats the standard library can't generate.
func literalFixtures() []fixture {
	// Built at run time: GitHub's push protection takes a literal 40-character
	// key next to aws_secret_access_key for a real one.
	awsReal := strings.Repeat("Zk3vP9qR", 5)
	const (
		awsDocs  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
		wgKey    = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
		ageKey   = "AGE-SECRET-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSQ7QJ9Q"
		ageRecip = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
		token    = "tund_0123456789abcdef0123456789abcdef01234567"
	)
	s := func(name, data, marker string) fixture { return fixture{name, []byte(data), marker} }
	w := func(name, data, marker string) fixture { return fixture{name, utf16LE([]byte(data)), marker} }
	return []fixture{
		// Text keys.
		s("secret.asc", "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nlQOYBGU\n-----END PGP PRIVATE KEY BLOCK-----\n", "private key"),
		s("enc.pem", "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIFHDBOBgkq\n-----END ENCRYPTED PRIVATE KEY-----\n", "private key"),
		s("host.key", "-----BEGIN NEBULA X25519 PRIVATE KEY-----\nAAAA\n-----END NEBULA X25519 PRIVATE KEY-----\n", "private key"),
		s("tectia.key", "---- BEGIN SSH2 ENCRYPTED PRIVATE KEY ----\nComment: \"2048-bit rsa\"\nP2/56wAAA\n---- END SSH2 ENCRYPTED PRIVATE KEY ----\n", "private key"),
		s("ssh2.txt", "---- BEGIN SSH2 PRIVATE KEY ----\nP2/56wAAA\n", "private key"),
		s("work.txt", "PuTTY-User-Key-File-3: ssh-ed25519\nEncryption: none\nComment: eddsa-key-20240101\n", "PuTTY key"),
		s("old.txt", "PuTTY-User-Key-File-2: ssh-rsa\r\nEncryption: aes256-cbc\r\n", "PuTTY key"),
		s("keys.txt", "# created: 2024-05-01T10:00:00Z\n# public key: "+ageRecip+"\n"+ageKey+"\n", "age key"),
		s("ta.key", "#\n# 2048 bit OpenVPN static key\n#\n-----BEGIN OpenVPN Static key V1-----\ne685bdaf659a25a200e2b9e39e51ff03\n-----END OpenVPN Static key V1-----\n", "OpenVPN key"),
		s("client.conf", "client\nremote vpn.example.com 1194\n<tls-auth>\n-----BEGIN OpenVPN Static key V1-----\ne685bdaf\n</tls-auth>\n", "OpenVPN key"),
		s("wg0.conf", "[Interface]\nPrivateKey = "+wgKey+"\nAddress = 10.0.0.2/32\n", "WireGuard key"),
		s("wg.txt", "  privatekey="+wgKey+"\r\n", "WireGuard key"),
		s("aws.ini", "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = "+awsReal+"\n", "AWS secret key"),
		s("ci.yml", "env:\n  AWS_SECRET_ACCESS_KEY: \""+awsReal+"\"\n", "AWS secret key"),
		s("both.ini", "[docs]\naws_secret_access_key = "+awsDocs+"\n[prod]\naws_secret_access_key="+awsReal+"\n", "AWS secret key"),
		s("tund-backup.yml", "server: https://tund.example.com\nauthtoken: "+token+"\n", "tund authtoken"),
		s("config.yaml", "tunnels: {}\n  authtoken: '"+token+"'\n", "tund authtoken"),

		// AWS keys as environment variables: sh, PowerShell, cmd, Dockerfile,
		// Compose and JSON.
		s("env.sh", "#!/bin/sh\nexport AWS_SECRET_ACCESS_KEY="+awsReal+"\n", "AWS secret key"),
		s(".zshrc", "export  AWS_SECRET_ACCESS_KEY=\""+awsReal+"\"\n", "AWS secret key"),
		s("profile.ps1", "$env:AWS_SECRET_ACCESS_KEY = \""+awsReal+"\"\n", "AWS secret key"),
		s("aws.ps1", "$Env:AWS_SECRET_ACCESS_KEY='"+awsReal+"'\r\n", "AWS secret key"),
		s("aws.cmd", "@echo off\r\nset AWS_SECRET_ACCESS_KEY="+awsReal+"\r\n", "AWS secret key"),
		s("aws.bat", "set \"AWS_SECRET_ACCESS_KEY="+awsReal+"\"\r\n", "AWS secret key"),
		s("Dockerfile", "FROM alpine\nENV AWS_SECRET_ACCESS_KEY="+awsReal+"\n", "AWS secret key"),
		s("compose.yml", "services:\n  app:\n    environment:\n      - AWS_SECRET_ACCESS_KEY="+awsReal+"\n", "AWS secret key"),
		s("vars.json", "{\n  \"AWS_SECRET_ACCESS_KEY\": \""+awsReal+"\"\n}\n", "AWS secret key"),

		// A UTF-8 byte order mark before the first line.
		s("bom.env", "\xef\xbb\xbfAWS_SECRET_ACCESS_KEY="+awsReal+"\r\n", "AWS secret key"),
		s("bom.yml", "\xef\xbb\xbfauthtoken: "+token+"\n", "tund authtoken"),

		// UTF-16 text keys, as Windows PowerShell 5.1 redirects them.
		w("wg0-utf16.conf", "[Interface]\r\nPrivateKey = "+wgKey+"\r\n", "WireGuard key"),
		w("profile-utf16.ps1", "$env:AWS_SECRET_ACCESS_KEY = \""+awsReal+"\"\r\n", "AWS secret key"),
		w("secret-utf16.asc", "-----BEGIN PGP PRIVATE KEY BLOCK-----\r\n\r\nlQOYBGU\r\n-----END PGP PRIVATE KEY BLOCK-----\r\n", "private key"),
		w("keys-utf16.txt", "# public key: "+ageRecip+"\r\n"+ageKey+"\r\n", "age key"),
		w("tund-utf16.yml", "authtoken: "+token+"\r\n", "tund authtoken"),

		// Look alike, but aren't secrets.
		s("README.md", "Paste the block that starts with BEGIN PRIVATE KEY into the field.\n", ""),
		s("dhparam.pem", "-----BEGIN DH PARAMETERS-----\nMIIBCAKCAQEA\n-----END DH PARAMETERS-----\n", ""),
		s("public.asc", "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQINBGU\n-----END PGP PUBLIC KEY BLOCK-----\n", ""),
		s("message.asc", "-----BEGIN PGP MESSAGE-----\n\nhQEMA\n-----END PGP MESSAGE-----\n", ""),
		s("ssh2.pub", "---- BEGIN SSH2 PUBLIC KEY ----\nAAAAB3NzaC1yc2E\n---- END SSH2 PUBLIC KEY ----\n", ""),
		s("recipients.txt", "# public key: "+ageRecip+"\n", ""),
		s("short-age.txt", ageKey[:len(ageKey)-1]+"\n", ""),
		s("peer.conf", "[Peer]\nPublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=\nEndpoint = 192.0.2.1:51820\n", ""),
		s("wg-bad.conf", "PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmz=\n", ""), // impossible last character
		s("aws-docs.ini", "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = "+awsDocs+"\n", ""),
		s("deploy.sh", "# aws_secret_access_key = <your key>\n", ""),
		s("ci.yaml", "authtoken: ${TUND_AUTHTOKEN}\n", ""),
		s("notes.gpg", "Šifrované poznámky\n", ""), // text, though 0xC5 starts a PGP packet
		s("ref.sh", "export AWS_SECRET_ACCESS_KEY=\"$(pass show aws/secret)\"\n", ""),
		s("ref.ps1", "$env:AWS_SECRET_ACCESS_KEY = $secret\n", ""),
		s("ref.yml", "    environment:\n      - AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}\n", ""),
		s("docs.sh", "export AWS_SECRET_ACCESS_KEY="+awsDocs+"\n", ""),
		w("readme-utf16.txt", "Paste the block that starts with BEGIN PRIVATE KEY into the field.\r\n", ""),
		w("chinese-utf16.txt", "密钥放在保险箱里。\r\n", ""),
		{"layer1.mp1", []byte{0xff, 0xfe, 0x90, 0x44, 0x00, 0x00, 0x13, 0x37}, ""}, // binary that starts like a BOM

		// Binary OpenPGP packets.
		{"secret.gpg", []byte{0x95, 0x03, 0x2e, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x01, 0x08, 0x00, 0xc3}, "PGP secret key"},
		{"key.pgp", []byte{0xc5, 0x58, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x16, 0x09, 0x2b, 0x06, 0x00}, "PGP secret key"},
		{"key.sec", []byte{0x94, 0x2e, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x01, 0x08, 0x00}, "PGP secret key"},
		{"key.skr", []byte{0x97, 0x00, 0x00, 0x03, 0x2e, 0x04}, "PGP secret key"},
		{"secret.der", []byte{0x95, 0x03, 0x2e, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x01, 0x08, 0x00, 0xc3}, ""},
		{"secret-bin.asc", []byte{0x95, 0x03, 0x2e, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x01, 0x08, 0x00, 0xc3}, ""},
		{"pubring.gpg", []byte{0x99, 0x01, 0x0d, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x01, 0x08, 0x00}, ""},
		{"public.pgp", []byte{0xc6, 0x33, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x16, 0x09, 0x00}, ""},
		{"message.gpg", []byte{0x85, 0x01, 0x0c, 0x03, 0x00, 0x11}, ""},
		{"file.gpg", []byte{0x8c, 0x0d, 0x04, 0x09, 0x03, 0x08, 0x00}, ""},

		// PKCS #12 (version 3) under a DER or certificate name.
		{"store.der", []byte{0x30, 0x82, 0x0a, 0x1c, 0x02, 0x01, 0x03, 0x30, 0x82, 0x09, 0xd6, 0x06, 0x09, 0x00}, "private key"},
		{"store.crt", []byte{0x30, 0x82, 0x0a, 0x1c, 0x02, 0x01, 0x03, 0x30, 0x82, 0x09, 0xd6, 0x06, 0x09, 0x00}, "private key"},
		{"long.der", []byte{0x30, 0x84, 0x00, 0x00, 0x01, 0x00, 0x02, 0x01, 0x00}, ""}, // 4-byte lengths aren't used by keys
		{"empty.key", nil, ""},
	}
}

// recorder records how far Sniff reads.
type recorder struct {
	r   io.ReaderAt
	end int64 // highest offset read, exclusive
}

func (r *recorder) ReadAt(p []byte, off int64) (int, error) {
	r.end = max(r.end, off+int64(len(p)))
	return r.r.ReadAt(p, off)
}

func TestSniffReadsOnlyItsLimit(t *testing.T) {
	const header = "-----BEGIN PRIVATE KEY-----"
	text := func(n, keyAt int) []byte {
		b := bytes.Repeat([]byte("lorem ipsum\n"), n/12+1)[:n]
		if keyAt >= 0 {
			copy(b[keyAt:], header)
		}
		return b
	}
	binary := make([]byte, 3<<20)
	copy(binary, "\x00\x01-----BEGIN PRIVATE KEY-----")
	var deck bytes.Buffer
	zw := zip.NewWriter(&deck)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "Data/movie.mov", Method: zip.Store})
	w.Write(bytes.Repeat([]byte(header+"\n"), (3<<20)/len(header)))
	zw.Close()
	// In UTF-16 the limits still count bytes: a BOM, then two per character.
	wide := func(n, keyAt int) []byte { return utf16LE(text(n, keyAt)) }
	end := func(limit int) int { return limit/2 - 1 - len(header) } // the key ends at the limit

	for _, c := range []struct {
		name  string
		data  []byte
		size  int64 // passed to Sniff
		kind  SniffKind
		found bool
		limit int64 // Sniff must not read past this offset
	}{
		{"big.pem", text(3<<20, 100), 3 << 20, SniffDeep, true, DeepLimit},
		{"big.pem", text(3<<20, DeepLimit-len(header)), 3 << 20, SniffDeep, true, DeepLimit},
		{"big.pem", text(3<<20, DeepLimit-len(header)+1), 3 << 20, SniffDeep, false, DeepLimit},
		{"big.pem", text(3<<20, 2<<20), 3 << 20, SniffDeep, false, DeepLimit},
		{"notes.txt", text(200<<10, 1<<10), 200 << 10, SniffText, true, TextLimit},
		{"notes.txt", text(200<<10, TextLimit-len(header)+1), 200 << 10, SniffText, false, TextLimit},
		{"notes.txt", text(200<<10, 100<<10), 200 << 10, SniffText, false, TextLimit},
		{"short.pem", text(1<<20, 300), 400, SniffDeep, true, 400},        // size bounds the read
		{"short.pem", text(1<<20, 500), 400, SniffDeep, false, 400},       // the rest is never looked at
		{"truncated.pem", []byte(header), 4096, SniffDeep, true, 4096},    // the file shrank since its stat
		{"unknown.pem", text(3<<20, 100), -1, SniffDeep, true, DeepLimit}, // size unknown
		{"blob.pem", binary, 3 << 20, SniffDeep, false, binaryWindow},
		{"Deck.key", deck.Bytes(), int64(deck.Len()), SniffDeep, false, binaryWindow},
		{"empty.txt", nil, 0, SniffText, false, 0},
		{"skip.txt", text(100, 0), 100, SniffNone, false, 0},
		{"big16.pem", wide(1<<20, end(DeepLimit)), 2<<20 + 2, SniffDeep, true, DeepLimit},
		{"big16.pem", wide(1<<20, end(DeepLimit)+1), 2<<20 + 2, SniffDeep, false, DeepLimit},
		{"notes16.txt", wide(100<<10, end(TextLimit)), 200<<10 + 2, SniffText, true, TextLimit},
		{"notes16.txt", wide(100<<10, end(TextLimit)+1), 200<<10 + 2, SniffText, false, TextLimit},
	} {
		r := &recorder{r: bytes.NewReader(c.data)}
		_, found, err := Sniff(r, c.size, c.name, c.kind)
		if err != nil || found != c.found {
			t.Errorf("Sniff(%s, size %d, kind %d) = %v, %v; want %v", c.name, c.size, c.kind, found, err, c.found)
		}
		if r.end > c.limit {
			t.Errorf("Sniff(%s, size %d, kind %d) read up to %d, limit %d", c.name, c.size, c.kind, r.end, c.limit)
		}
	}
}

// Text with a byte order mark and mostly ASCII UTF-16 are wide; real binaries
// keep the binary path.
func TestWideText(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, binaryWindow)
	n, err := readFull(f, head)
	if err != nil || n < 64 {
		t.Fatalf("reading %s: %d bytes, %v", exe, n, err)
	}
	le := func(s string) []byte { return widen([]byte(s), 2, binary.LittleEndian, false) }
	for _, c := range []struct {
		name string
		head []byte
		text string // the decoded text; "" for not wide
	}{
		{"this test's executable", head[:n], ""},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x01\x00"), ""},
		{"gzip", []byte{0x1f, 0x8b, 0x08, 0x00, 0x8c, 0x3a, 0x6f, 0x65, 0x00, 0x03, 0xcb, 0x48}, ""},
		{"pgp secret key", []byte{0x95, 0x03, 0x2e, 0x04, 0x65, 0x1f, 0x2a, 0x3b, 0x01, 0x08, 0x00, 0xc3}, ""},
		{"utf-8", []byte("plain text, schön"), ""},
		{"empty", nil, ""},
		{"NUL", []byte{0}, ""},
		{"NUL character", le("ab\x00c"), ""},
		{"half ASCII", le("ab鍵鍵"), ""},
		{"utf-16le bom", widen([]byte("x鍵"), 2, binary.LittleEndian, true), "x鍵"},
		{"utf-16be bom", widen([]byte("x鍵"), 2, binary.BigEndian, true), "x鍵"},
		{"utf-32le bom", widen([]byte("x鍵"), 4, binary.LittleEndian, true), "x鍵"},
		{"utf-32be bom", widen([]byte("x鍵"), 4, binary.BigEndian, true), "x鍵"},
		{"utf-16le", le("abc"), "abc"},
		{"utf-16be", widen([]byte("abc"), 2, binary.BigEndian, false), "abc"},
		{"three in four ASCII", le("abc鍵"), "abc鍵"},
	} {
		enc := wideText(c.head)
		if enc == nil {
			if c.text != "" {
				t.Errorf("wideText(%s) = nil, want %q", c.name, c.text)
			}
			continue
		}
		got, err := enc.NewDecoder().Bytes(c.head)
		if c.text == "" || err != nil || string(got) != c.text {
			t.Errorf("wideText(%s) decodes to %q, %v; want %q", c.name, got, err, c.text)
		}
	}
}

// failAfter fails reads that reach past off.
type failAfter struct {
	data []byte
	off  int64
}

var errDisk = errors.New("input/output error")

func (f failAfter) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.off {
		return 0, errDisk
	}
	return copy(p, f.data[off:]), nil
}

func TestSniffReadError(t *testing.T) {
	data := bytes.Repeat([]byte("lorem ipsum\n"), 10<<10)
	for _, off := range []int64{0, binaryWindow} {
		r := failAfter{data, off}
		if _, found, err := Sniff(r, int64(len(data)), "big.pem", SniffDeep); !errors.Is(err, errDisk) || found {
			t.Errorf("failing after %d: Sniff = %v, %v; want the read error", off, found, err)
		}
	}
}
