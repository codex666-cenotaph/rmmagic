package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestWebhookSignatureRoundTrip(t *testing.T) {
	secret := []byte("super-secret")
	body := []byte(`{"event":"alert.fired"}`)

	var gotSig string
	var gotTS int64
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-RMM-Signature")
		gotTS, _ = strconv.ParseInt(r.Header.Get("X-RMM-Timestamp"), 10, 64)
		gotBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := New(SMTPConfig{})
	if err := n.SendWebhook(context.Background(), srv.URL, secret, body); err != nil {
		t.Fatal(err)
	}
	if !VerifySignature(secret, gotTS, gotBody, gotSig) {
		t.Fatal("signature did not verify")
	}
	if VerifySignature([]byte("wrong"), gotTS, gotBody, gotSig) {
		t.Fatal("signature verified with the wrong secret")
	}
	if VerifySignature(secret, gotTS+1, gotBody, gotSig) {
		t.Fatal("signature verified with a tampered timestamp")
	}
}

func TestWebhookNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	n := New(SMTPConfig{})
	if err := n.SendWebhook(context.Background(), srv.URL, []byte("s"), []byte("{}")); err == nil {
		t.Fatal("want error on 500")
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		leaked = true
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	n := New(SMTPConfig{})
	err := n.SendWebhook(context.Background(), srv.URL, []byte("s"), []byte("{}"))
	if err == nil {
		t.Fatal("redirect must surface as a delivery error")
	}
	if leaked {
		t.Fatal("signed body was re-sent to the redirect target")
	}
}

func TestEmailRequiresConfig(t *testing.T) {
	n := New(SMTPConfig{})
	if err := n.SendEmail([]string{"a@b.test"}, "s", "b"); err == nil {
		t.Fatal("want error without smtp config")
	}
	n = New(SMTPConfig{Addr: "localhost:1025", From: "rmm@test"})
	if err := n.SendEmail([]string{"evil\r\nRCPT TO:<x>"}, "s", "b"); err == nil {
		t.Fatal("CRLF in recipient must be rejected")
	}
}
