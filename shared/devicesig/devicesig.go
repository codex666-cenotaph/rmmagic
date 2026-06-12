// Package devicesig defines the device identity signature scheme shared
// by agent and server: Ed25519 keys generated on the device at
// enrollment. Two operations exist — signing the connection challenge
// nonce, and signing HTTP ingest requests (timestamp + body hash, to
// bound replay).
package devicesig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"strconv"
	"time"
)

// MaxSkew is how far an ingest request timestamp may deviate from
// server time before it is rejected.
const MaxSkew = 5 * time.Minute

// Fingerprint identifies a public key: sha256 of the raw key bytes.
func Fingerprint(pub ed25519.PublicKey) []byte {
	h := sha256.Sum256(pub)
	return h[:]
}

// SignChallenge signs the gateway connection nonce.
func SignChallenge(priv ed25519.PrivateKey, nonce []byte) []byte {
	return ed25519.Sign(priv, challengeMessage(nonce))
}

func VerifyChallenge(pub ed25519.PublicKey, nonce, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, challengeMessage(nonce), sig)
}

// The domain-separation prefixes ensure a signature for one purpose can
// never be replayed for the other.
func challengeMessage(nonce []byte) []byte {
	return append([]byte("rmm-connect-v1:"), nonce...)
}

// SignRequest signs an HTTP ingest request: unix timestamp and the
// sha256 of the request body.
func SignRequest(priv ed25519.PrivateKey, unixTS int64, body []byte) []byte {
	return ed25519.Sign(priv, requestMessage(unixTS, body))
}

func VerifyRequest(pub ed25519.PublicKey, unixTS int64, body, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, requestMessage(unixTS, body), sig)
}

func requestMessage(unixTS int64, body []byte) []byte {
	h := sha256.Sum256(body)
	msg := append([]byte("rmm-request-v1:"), strconv.FormatInt(unixTS, 10)...)
	msg = append(msg, ':')
	return append(msg, h[:]...)
}
