package auth

import (
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTP creates a new TOTP key for enrollment. Returns the
// shared secret and the otpauth:// provisioning URL.
func GenerateTOTP(issuer, account string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Algorithm:   otp.AlgorithmSHA1, // broadest authenticator compatibility
		Digits:      otp.DigitsSix,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func ValidateTOTP(code, secret string) bool {
	return totp.Validate(code, secret)
}
