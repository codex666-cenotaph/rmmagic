package devicesig

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestChallengeRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sig := SignChallenge(priv, nonce)
	if !VerifyChallenge(pub, nonce, sig) {
		t.Fatal("valid challenge signature rejected")
	}
	nonce[0] ^= 1
	if VerifyChallenge(pub, nonce, sig) {
		t.Fatal("tampered nonce accepted")
	}
}

func TestRequestRoundTripAndDomainSeparation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"samples":[]}`)
	ts := time.Now().Unix()

	sig := SignRequest(priv, ts, body)
	if !VerifyRequest(pub, ts, body, sig) {
		t.Fatal("valid request signature rejected")
	}
	if VerifyRequest(pub, ts+1, body, sig) {
		t.Fatal("timestamp tamper accepted")
	}
	if VerifyRequest(pub, ts, []byte(`{}`), sig) {
		t.Fatal("body tamper accepted")
	}
	// A challenge signature must never verify as a request signature.
	nonce := []byte("rmm-request-v1:123:abc")
	if VerifyRequest(pub, ts, body, SignChallenge(priv, nonce)) {
		t.Fatal("cross-domain signature accepted")
	}
}
