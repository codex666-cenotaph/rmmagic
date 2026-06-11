package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// dummyHash equalizes login timing when the email is unknown.
var dummyHash, _ = auth.HashPassword("dummy-password-for-timing")

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password required")
		return
	}

	ip := clientIP(r)
	if !s.loginLimiter.Allow(ip+"|"+req.Email) || !s.loginLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}

	ctx := r.Context()
	var u store.AuthUser
	err := s.Store.System(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = store.LookupUserByEmail(ctx, tx, req.Email)
		return err
	})
	if err != nil {
		auth.VerifyPassword(req.Password, dummyHash) // constant-ish timing
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.VerifyPassword(req.Password, u.PasswordHash) {
		_ = s.Store.WithTenant(ctx, u.TenantID, func(tx pgx.Tx) error {
			return store.InsertAudit(ctx, tx, u.TenantID, store.AuditEntry{
				ActorType: "user", ActorID: &u.UserID, Action: "user.login_failed", IP: &ip,
			})
		})
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if u.Status != "active" || u.TenantStatus != "active" {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	mfaPassed := !u.MFAEnabled
	err = s.Store.WithTenant(ctx, u.TenantID, func(tx pgx.Tx) error {
		if err := store.CreateSession(ctx, tx, hash, u.TenantID, u.UserID, mfaPassed, ip, s.SessionTTL); err != nil {
			return err
		}
		action := "user.login"
		if !mfaPassed {
			action = "user.login_mfa_pending"
		}
		return store.InsertAudit(ctx, tx, u.TenantID, store.AuditEntry{
			ActorType: "user", ActorID: &u.UserID, Action: action, IP: &ip,
		})
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]bool{"mfa_required": !mfaPassed})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	si, ok := sessionFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Code = strings.TrimSpace(req.Code)

	ip := clientIP(r)
	if !s.loginLimiter.Allow("mfa|" + si.UserID.String()) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}

	ctx := r.Context()
	err := s.Store.WithTenant(ctx, si.TenantID, func(tx pgx.Tx) error {
		u, err := store.GetUser(ctx, tx, si.UserID)
		if err != nil {
			return err
		}
		if !u.MFAEnabled || len(u.MFASecretEnc) == 0 {
			return errors.New("mfa not enabled")
		}
		secret, err := s.Box.Open(u.MFASecretEnc, []byte(u.ID.String()))
		if err != nil {
			return err
		}
		if !auth.ValidateTOTP(req.Code, string(secret)) {
			// Fall back to a recovery code.
			if err := store.ConsumeRecoveryCode(ctx, tx, u.ID, auth.HashRecoveryCode(req.Code)); err != nil {
				return errBadCode
			}
		}
		if err := store.UpgradeSessionMFA(ctx, tx, si.TokenHash); err != nil {
			return err
		}
		return store.InsertAudit(ctx, tx, si.TenantID, store.AuditEntry{
			ActorType: "user", ActorID: &si.UserID, Action: "user.mfa_verified", IP: &ip,
		})
	})
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid code")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

var errBadCode = errors.New("bad mfa code")

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	si, ok := sessionFrom(ctx)
	if ok {
		_ = s.Store.WithTenant(ctx, si.TenantID, func(tx pgx.Tx) error {
			return store.DeleteSession(ctx, tx, si.TokenHash)
		})
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	type grantJSON struct {
		ScopeType   string     `json:"scope_type"`
		ScopeID     *uuid.UUID `json:"scope_id"`
		Permissions []string   `json:"permissions"`
	}
	var resp struct {
		User struct {
			ID         uuid.UUID `json:"id"`
			Email      string    `json:"email"`
			MFAEnabled bool      `json:"mfa_enabled"`
		} `json:"user"`
		Tenant struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
			Slug string    `json:"slug"`
		} `json:"tenant"`
		Grants []grantJSON `json:"grants"`
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		u, err := store.GetUser(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		t, err := store.GetTenant(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		resp.User.ID, resp.User.Email, resp.User.MFAEnabled = u.ID, u.Email, u.MFAEnabled
		resp.Tenant.ID, resp.Tenant.Name, resp.Tenant.Slug = t.ID, t.Name, t.Slug
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	resp.Grants = []grantJSON{}
	for _, g := range p.Grants {
		gj := grantJSON{ScopeType: string(g.Scope.Type)}
		if g.Scope.Type != auth.ScopeTenant {
			id := g.Scope.ID
			gj.ScopeID = &id
		}
		for _, perm := range g.Permissions {
			gj.Permissions = append(gj.Permissions, string(perm))
		}
		resp.Grants = append(resp.Grants, gj)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var resp struct {
		Secret     string `json:"secret"`
		OTPAuthURL string `json:"otpauth_url"`
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		u, err := store.GetUser(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if u.MFAEnabled {
			return errors.New("mfa already enabled")
		}
		secret, url, err := auth.GenerateTOTP(s.TOTPIssuer, u.Email)
		if err != nil {
			return err
		}
		enc, err := s.Box.Seal([]byte(secret), []byte(u.ID.String()))
		if err != nil {
			return err
		}
		if err := store.SetUserMFASecret(ctx, tx, u.ID, enc); err != nil {
			return err
		}
		resp.Secret, resp.OTPAuthURL = secret, url
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "mfa setup failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip := clientIP(r)

	var resp struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		u, err := store.GetUser(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if u.MFAEnabled || len(u.MFASecretEnc) == 0 {
			return errors.New("setup required first")
		}
		secret, err := s.Box.Open(u.MFASecretEnc, []byte(u.ID.String()))
		if err != nil {
			return err
		}
		if !auth.ValidateTOTP(strings.TrimSpace(req.Code), string(secret)) {
			return errBadCode
		}
		if err := store.EnableUserMFA(ctx, tx, u.ID); err != nil {
			return err
		}
		codes, hashes, err := auth.NewRecoveryCodes(8)
		if err != nil {
			return err
		}
		if err := store.AddRecoveryCodes(ctx, tx, p.TenantID, u.ID, hashes); err != nil {
			return err
		}
		resp.RecoveryCodes = codes
		return store.InsertAudit(ctx, tx, p.TenantID, store.AuditEntry{
			ActorType: "user", ActorID: &u.ID, Action: "user.mfa_enabled", IP: &ip,
			Details: mustJSON(map[string]any{"email": u.Email}),
		})
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
