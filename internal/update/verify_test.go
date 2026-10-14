package update

import (
	"crypto/rand"
	"errors"
	"testing"

	"aead.dev/minisign"
)

func TestEmbeddedKeyIsTheReleaseKey(t *testing.T) {
	ids := KeyIDs()
	if len(ids) != 1 || ids[0] != "93E83CF19144CC30" {
		t.Fatalf("embedded keys = %v, want [93E83CF19144CC30]", ids)
	}
}

func TestParseKeys(t *testing.T) {
	pub, _, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	txt, _ := pub.MarshalText()
	for name, in := range map[string]string{
		"bare":     string(txt),
		"comments": "# the key\nuntrusted comment: minisign public key X\n" + string(txt) + "\n\n",
		"crlf":     "untrusted comment: x\r\n" + string(txt) + "\r\n",
		"two keys": string(txt) + "\n" + string(txt),
		"padded":   "  " + string(txt) + "  ",
	} {
		t.Run(name, func(t *testing.T) {
			keys, err := ParseKeys(in)
			if err != nil || len(keys) == 0 || keys[0].ID() != pub.ID() {
				t.Fatalf("ParseKeys = %v, %v", keys, err)
			}
		})
	}
	for name, in := range map[string]string{"empty": "", "only comments": "# nothing\n", "garbage": "not base64!!"} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseKeys(in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestVerify(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := minisign.GenerateKey(rand.Reader)
	msg := []byte("abc  outfit_1.0.0_linux_amd64.tar.gz\n")
	sig := minisign.Sign(priv, msg)

	if err := Verify([]minisign.PublicKey{pub}, msg, sig); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}
	if err := Verify([]minisign.PublicKey{other, pub}, msg, sig); err != nil {
		t.Fatalf("rotation (second key signs) rejected: %v", err)
	}
	cases := map[string]struct {
		keys []minisign.PublicKey
		msg  []byte
		sig  []byte
	}{
		"tampered message": {[]minisign.PublicKey{pub}, append([]byte("x"), msg...), sig},
		"wrong key":        {[]minisign.PublicKey{other}, msg, sig},
		"no keys":          {nil, msg, sig},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Verify(c.keys, c.msg, c.sig); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
	for name, bad := range map[string][]byte{"empty": nil, "garbage": []byte("nope"), "truncated": sig[:len(sig)/2]} {
		t.Run(name, func(t *testing.T) {
			if err := Verify([]minisign.PublicKey{pub}, msg, bad); err == nil || errors.Is(err, ErrBadSignature) && name != "truncated" {
				t.Fatalf("err = %v, want an unreadable-signature error", err)
			}
		})
	}
}
