package pwhash

import "testing"

func TestRoundTrip(t *testing.T) {
	h, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify("correct horse", h) || Verify("wrong", h) || Verify("x", "garbage") {
		t.Fatal("verify mismatch")
	}
}

// Hash produced by the dashboard (node:crypto scrypt) must verify in Go.
func TestNodeCompat(t *testing.T) {
	// node -e 'const c=require("crypto");const s=Buffer.from("0123456789abcdef");console.log("scrypt$16384$8$1$"+s.toString("base64url")+"$"+c.scryptSync("pw",s,32,{N:16384,r:8,p:1}).toString("base64url"))'
	const h = "scrypt$16384$8$1$MDEyMzQ1Njc4OWFiY2RlZg$o6nuri0UB0zAYtUPW1FsUcO86OUVadX-DXiUh65Fhto"
	if !Verify("pw", h) {
		t.Fatal("node-generated hash did not verify")
	}
}
