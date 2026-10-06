package secretfile

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"path"
	"regexp"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
)

// SniffKind says how deeply to look into a regular file's content before
// serving it.
type SniffKind int

const (
	SniffNone SniffKind = iota // don't look
	SniffDeep                  // key- and certificate-like extensions
	SniffText                  // other small text files
)

// How much of a file Sniff reads at most.
const (
	DeepLimit = 1 << 20
	TextLimit = 64 << 10
)

// binaryWindow is the part of a file that decides whether it is binary: a NUL
// byte in it means binary, unless the file is UTF-16 or UTF-32 text (see
// wideText).
const binaryWindow = 8 << 10

// deepExts hold keys and certificates; their content is checked whatever
// their size.
var deepExts = map[string]bool{
	".pem": true, ".key": true, ".crt": true, ".cer": true, ".cert": true, ".der": true,
	".asc": true, ".gpg": true, ".pgp": true, ".sec": true, ".skr": true,
}

// derExts may hold binary DER: keys, certificates and PKCS #12 files.
var derExts = map[string]bool{".pem": true, ".key": true, ".crt": true, ".cer": true, ".cert": true, ".der": true}

// SniffFor returns how to sniff a regular file named name with the given
// size: SniffDeep for key- and certificate-like extensions (any size; reads
// at most DeepLimit), SniffText for other files up to TextLimit bytes,
// SniffNone otherwise.
func SniffFor(name string, size int64) SniffKind {
	switch {
	case deepExt(name) != "":
		return SniffDeep
	case size <= TextLimit:
		return SniffText
	}
	return SniffNone
}

// deepExt returns the key or certificate extension of name, also behind
// backup markers ("server.key.bak", "key.pem 2"), or "".
func deepExt(name string) string {
	for k := range variants(Fold(name)) {
		if ext := path.Ext(k); deepExts[ext] {
			return ext
		}
	}
	return ""
}

// Sniff looks for secret markers in the first bytes of r (at most DeepLimit
// for SniffDeep, TextLimit for SniffText). marker names what was found
// ("private key", "WireGuard key", ...). Sniff never reads past size, unless
// size is negative (unknown).
func Sniff(r io.ReaderAt, size int64, name string, kind SniffKind) (marker string, found bool, err error) {
	var limit int64
	switch kind {
	case SniffDeep:
		limit = DeepLimit
	case SniffText:
		limit = TextLimit
	default:
		return "", false, nil
	}
	if size >= 0 {
		limit = min(limit, size)
	}
	src := io.NewSectionReader(r, 0, limit)
	buf := make([]byte, limit)
	n, err := readFull(src, buf[:min(limit, binaryWindow)])
	if err != nil {
		return "", false, err
	}
	head := buf[:n]
	if bytes.HasPrefix(head, []byte("PK\x03\x04")) {
		return "", false, nil // a zip, such as a Keynote .key
	}
	ext := deepExt(name)
	// Before the NUL test: a DER key needn't hold a NUL byte (an EC key
	// without its public part), and its control bytes never start a text.
	if derExts[ext] && derPrivateKey(head) {
		return "private key", true, nil
	}
	wide := wideText(head)
	if wide == nil && bytes.IndexByte(head, 0) >= 0 {
		if (ext == ".gpg" || ext == ".pgp" || ext == ".sec" || ext == ".skr") && pgpSecretKey(head) {
			return "PGP secret key", true, nil
		}
		return "", false, nil
	}
	if n == binaryWindow {
		m, err := readFull(src, buf[n:])
		if err != nil {
			return "", false, err
		}
		n += m
	}
	text := buf[:n]
	if wide != nil {
		// The limits count the bytes read, not the decoded ones.
		if text, err = wide.NewDecoder().Bytes(text); err != nil {
			return "", false, err
		}
	}
	marker, found = textMarker(bytes.TrimPrefix(text, []byte("\xef\xbb\xbf"))) // a UTF-8 BOM
	return marker, found, nil
}

// wideText returns the encoding of UTF-16 or UTF-32 text (Windows PowerShell
// 5.1 writes UTF-16 by default), known by its byte order mark, or for UTF-16
// without one by its NUL bytes: at least three in four code units of head
// are ASCII (one byte NUL, the other not), and none is NUL. It returns nil
// for UTF-8 text and binaries.
func wideText(head []byte) encoding.Encoding {
	switch {
	case bytes.HasPrefix(head, []byte{0xff, 0xfe, 0, 0}):
		return utf32.UTF32(utf32.LittleEndian, utf32.ExpectBOM)
	case bytes.HasPrefix(head, []byte{0, 0, 0xfe, 0xff}):
		return utf32.UTF32(utf32.BigEndian, utf32.ExpectBOM)
	case bytes.HasPrefix(head, []byte{0xff, 0xfe}):
		return unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM)
	case bytes.HasPrefix(head, []byte{0xfe, 0xff}):
		return unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM)
	case bytes.IndexByte(head, 0) < 0:
		return nil
	}
	var le, be int
	for i := 0; i+1 < len(head); i += 2 {
		switch {
		case head[i] == 0 && head[i+1] == 0:
			return nil
		case head[i+1] == 0:
			le++
		case head[i] == 0:
			be++
		}
	}
	switch units := len(head) / 2; {
	case le > 0 && 4*le >= 3*units:
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	case be > 0 && 4*be >= 3*units:
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	}
	return nil
}

// readFull reads until b is full or the file ends.
func readFull(r io.Reader, b []byte) (int, error) {
	n, err := io.ReadFull(r, b)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	return n, err
}

// derPrivateKey reports whether b starts like a DER private key (PKCS #1,
// PKCS #8, SEC 1) or PKCS #12 file: a SEQUENCE whose first element is the
// version, INTEGER 0 to 3. Certificates start with a nested SEQUENCE instead.
func derPrivateKey(b []byte) bool {
	if len(b) < 2 || b[0] != 0x30 {
		return false
	}
	i := 2
	switch l := b[1]; {
	case l < 0x80:
	case l >= 0x81 && l <= 0x83:
		i += int(l - 0x80)
	default:
		return false
	}
	return len(b) >= i+3 && b[i] == 0x02 && b[i+1] == 0x01 && b[i+2] <= 0x03
}

// pgpSecretKey reports whether b starts with an OpenPGP secret-key packet
// (tag 5 in the old or the new packet format).
func pgpSecretKey(b []byte) bool {
	return len(b) > 0 && (b[0] >= 0x94 && b[0] <= 0x97 || b[0] == 0xc5)
}

var (
	rePEMKey    = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)
	reSSH2Key   = regexp.MustCompile(`-{4,5} ?BEGIN SSH2 (?:ENCRYPTED )?PRIVATE KEY ?-{4,5}`)
	rePuTTYKey  = regexp.MustCompile(`PuTTY-User-Key-File-[0-9]+:`)
	reAgeKey    = regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]{58}`)
	rePEMBase64 = regexp.MustCompile(`LS0tLS1CRUdJTi[A-Za-z0-9+/]{0,96}`) // "-----BEGIN" in base64
	reWireGuard = regexp.MustCompile(`(?im)^\s*PrivateKey\s*=\s*[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]=`)
	// Also the environment variable in sh, cmd, PowerShell, Dockerfiles,
	// Compose lists and JSON: export, set, ENV, $env:, "- " and quotes.
	reAWSSecret = regexp.MustCompile(`(?im)^\s*(?:(?:export|set|env)\s+|\$env:|-\s+)?["']?` +
		`aws_secret_access_key["']?\s*[=:]\s*["']?[A-Za-z0-9/+]{40}`)
	reTundToken = regexp.MustCompile(`(?m)^\s*authtoken:\s*["']?tund_`)
)

// textMarkers are checked in order. hint is a lower-case text each marker
// needs: a cheap test on the lower-cased (ASCII) content before the regexp.
var textMarkers = []struct {
	name, hint string
	match      func([]byte) bool
}{
	{"private key", "private key", rePEMKey.Match},
	{"private key", "begin ssh2", reSSH2Key.Match},
	{"PuTTY key", "putty-user-key-file-", rePuTTYKey.Match},
	{"age key", "age-secret-key-1", reAgeKey.Match},
	{"private key", "ls0tls1crudjti", base64PEMKey}, // kubeconfig client-key-data
	{"OpenVPN key", "-----begin openvpn static key v1-----", func(b []byte) bool {
		return bytes.Contains(b, []byte("-----BEGIN OpenVPN Static key V1-----"))
	}},
	{"WireGuard key", "privatekey", reWireGuard.Match},
	{"AWS secret key", "aws_secret_access_key", awsSecretKey},
	{"tund authtoken", "authtoken:", reTundToken.Match}, // a tund config under another name
}

func textMarker(b []byte) (string, bool) {
	lower := make([]byte, len(b))
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower[i] = c
	}
	for _, m := range textMarkers {
		if bytes.Contains(lower, []byte(m.hint)) && m.match(b) {
			return m.name, true
		}
	}
	return "", false
}

// base64PEMKey finds PEM private keys inside base64, as kubeconfig and
// Kubernetes secrets store them.
func base64PEMKey(b []byte) bool {
	for _, m := range rePEMBase64.FindAll(b, -1) {
		dec, err := base64.StdEncoding.AppendDecode(nil, m[:len(m)/4*4])
		if err == nil && rePEMKey.Match(dec) {
			return true
		}
	}
	return false
}

// awsSecretKey finds AWS secret access keys except the documentation's
// example keys.
func awsSecretKey(b []byte) bool {
	for _, m := range reAWSSecret.FindAll(b, -1) {
		if !bytes.Contains(m, []byte("EXAMPLE")) {
			return true
		}
	}
	return false
}
