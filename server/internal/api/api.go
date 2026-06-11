// Package api implements the REST API for the dashboard and public API
// clients. Every route is declared in the central registry with its
// required permission; a test asserts the invariant, so an endpoint
// without an authorization declaration cannot exist.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/secrets"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// PermSelf marks routes any fully authenticated principal may call
// (own-account operations); no RBAC permission is required.
const PermSelf auth.Permission = "self"

type Server struct {
	Store        *store.Store
	Box          *secrets.Box
	Log          *slog.Logger
	CookieSecure bool
	SessionTTL   time.Duration
	TOTPIssuer   string

	loginLimiter *rateLimiter
}

func NewServer(st *store.Store, box *secrets.Box, log *slog.Logger, cookieSecure bool) *Server {
	return &Server{
		Store:        st,
		Box:          box,
		Log:          log,
		CookieSecure: cookieSecure,
		SessionTTL:   12 * time.Hour,
		TOTPIssuer:   "rmmagic",
		loginLimiter: newRateLimiter(10, time.Minute),
	}
}

// Route is one API endpoint. Exactly one of Public or a non-empty Perm
// must be set; AllowPendingMFA additionally admits sessions that have
// not yet completed the MFA step (only the MFA/logout endpoints).
type Route struct {
	Method          string
	Pattern         string
	Public          bool
	Perm            auth.Permission
	AllowPendingMFA bool
	Handler         http.HandlerFunc
}

func (s *Server) Routes() []Route {
	return []Route{
		{Method: "POST", Pattern: "/api/v1/auth/login", Public: true, Handler: s.handleLogin},
		{Method: "POST", Pattern: "/api/v1/auth/mfa/verify", Perm: PermSelf, AllowPendingMFA: true, Handler: s.handleMFAVerify},
		{Method: "POST", Pattern: "/api/v1/auth/logout", Perm: PermSelf, AllowPendingMFA: true, Handler: s.handleLogout},
		{Method: "GET", Pattern: "/api/v1/auth/me", Perm: PermSelf, Handler: s.handleMe},
		{Method: "POST", Pattern: "/api/v1/auth/mfa/setup", Perm: PermSelf, Handler: s.handleMFASetup},
		{Method: "POST", Pattern: "/api/v1/auth/mfa/enable", Perm: PermSelf, Handler: s.handleMFAEnable},

		{Method: "GET", Pattern: "/api/v1/customers", Perm: auth.PermOrgRead, Handler: s.handleListCustomers},
		{Method: "POST", Pattern: "/api/v1/customers", Perm: auth.PermOrgManage, Handler: s.handleCreateCustomer},
		{Method: "PATCH", Pattern: "/api/v1/customers/{id}", Perm: auth.PermOrgManage, Handler: s.handleRenameCustomer},
		{Method: "DELETE", Pattern: "/api/v1/customers/{id}", Perm: auth.PermOrgManage, Handler: s.handleDeleteCustomer},
		{Method: "GET", Pattern: "/api/v1/customers/{id}/sites", Perm: auth.PermOrgRead, Handler: s.handleListSites},
		{Method: "POST", Pattern: "/api/v1/customers/{id}/sites", Perm: auth.PermOrgManage, Handler: s.handleCreateSite},
		{Method: "PATCH", Pattern: "/api/v1/sites/{id}", Perm: auth.PermOrgManage, Handler: s.handleUpdateSite},
		{Method: "DELETE", Pattern: "/api/v1/sites/{id}", Perm: auth.PermOrgManage, Handler: s.handleDeleteSite},

		{Method: "GET", Pattern: "/api/v1/users", Perm: auth.PermUsersRead, Handler: s.handleListUsers},
		{Method: "POST", Pattern: "/api/v1/users", Perm: auth.PermUsersManage, Handler: s.handleCreateUser},
		{Method: "PATCH", Pattern: "/api/v1/users/{id}", Perm: auth.PermUsersManage, Handler: s.handleUpdateUser},
		{Method: "GET", Pattern: "/api/v1/roles", Perm: auth.PermUsersRead, Handler: s.handleListRoles},
		{Method: "POST", Pattern: "/api/v1/users/{id}/assignments", Perm: auth.PermUsersManage, Handler: s.handleCreateAssignment},
		{Method: "DELETE", Pattern: "/api/v1/assignments/{id}", Perm: auth.PermUsersManage, Handler: s.handleDeleteAssignment},

		{Method: "GET", Pattern: "/api/v1/api-tokens", Perm: auth.PermTokensManage, Handler: s.handleListAPITokens},
		{Method: "POST", Pattern: "/api/v1/api-tokens", Perm: auth.PermTokensManage, Handler: s.handleCreateAPIToken},
		{Method: "DELETE", Pattern: "/api/v1/api-tokens/{id}", Perm: auth.PermTokensManage, Handler: s.handleRevokeAPIToken},

		{Method: "GET", Pattern: "/api/v1/audit", Perm: auth.PermAuditRead, Handler: s.handleListAudit},
	}
}

// Handler builds the http.Handler with per-route auth middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.Routes() {
		h := rt.Handler
		if !rt.Public {
			h = s.requireAuth(rt, h)
		}
		mux.Handle(rt.Method+" "+rt.Pattern, securityHeaders(h))
	}
	return mux
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// --- request context ---

type sessionInfo struct {
	TenantID  uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	MFAPassed bool
}

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxIP
)

func sessionFrom(ctx context.Context) (*sessionInfo, bool) {
	si, ok := ctx.Value(ctxSession).(*sessionInfo)
	return si, ok
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requireAuth authenticates the request (session cookie or bearer API
// token), builds the Principal with its grants, and pre-checks that the
// declared route permission is held somewhere. Handlers still perform
// the fine-grained scope check against the specific resource.
func (s *Server) requireAuth(rt Route, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxIP, clientIP(r))

		if tok := bearerToken(r); tok != "" && auth.IsAPIToken(tok) {
			p, err := s.authAPIToken(ctx, tok)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			s.dispatch(w, r.WithContext(auth.WithPrincipal(ctx, p)), rt)
			return
		}

		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		hash := auth.HashToken(cookie.Value)

		var sess store.AuthSession
		err = s.Store.System(ctx, func(tx pgx.Tx) error {
			var err error
			sess, err = store.LookupSession(ctx, tx, hash)
			return err
		})
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		si := &sessionInfo{TenantID: sess.TenantID, UserID: sess.UserID, TokenHash: hash, MFAPassed: sess.MFAPassed}
		ctx = context.WithValue(ctx, ctxSession, si)

		if !sess.MFAPassed {
			if !rt.AllowPendingMFA {
				writeError(w, http.StatusUnauthorized, "mfa required")
				return
			}
			// Pending sessions get no grants.
			next(w, r.WithContext(ctx))
			return
		}

		p, err := s.principalForUser(ctx, sess.TenantID, sess.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		s.dispatch(w, r.WithContext(auth.WithPrincipal(ctx, p)), rt)
	}
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, rt Route) {
	if rt.Perm != PermSelf {
		p, _ := auth.PrincipalFrom(r.Context())
		if p == nil || !p.Has(rt.Perm) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	rt.Handler(w, r)
}

func (s *Server) principalForUser(ctx context.Context, tenantID, userID uuid.UUID) (*auth.Principal, error) {
	p := &auth.Principal{TenantID: tenantID, UserID: userID}
	err := s.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := store.GetUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if u.Status != "active" {
			return errors.New("user not active")
		}
		assignments, perms, err := store.GrantsForUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		for i, a := range assignments {
			g := auth.Grant{Scope: auth.Scope{Type: auth.ScopeType(a.ScopeType)}}
			if a.ScopeID != nil {
				g.Scope.ID = *a.ScopeID
			}
			for _, ps := range perms[i] {
				g.Permissions = append(g.Permissions, auth.Permission(ps))
			}
			p.Grants = append(p.Grants, g)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Server) authAPIToken(ctx context.Context, token string) (*auth.Principal, error) {
	hash := auth.HashToken(token)
	var t store.AuthAPIToken
	err := s.Store.System(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = store.LookupAPIToken(ctx, tx, hash)
		return err
	})
	if err != nil {
		return nil, err
	}
	if t.RevokedAt != nil || (t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now())) {
		return nil, errors.New("token revoked or expired")
	}

	p := &auth.Principal{TenantID: t.TenantID, UserID: t.UserID, APITokenID: &t.TokenID}
	g := auth.Grant{Scope: auth.Scope{Type: auth.ScopeType(t.ScopeType)}}
	if t.ScopeID != nil {
		g.Scope.ID = *t.ScopeID
	}
	for _, ps := range t.Permissions {
		g.Permissions = append(g.Permissions, auth.Permission(ps))
	}
	p.Grants = []auth.Grant{g}

	// The token only works while its owner is still active.
	err = s.Store.WithTenant(ctx, t.TenantID, func(tx pgx.Tx) error {
		u, err := store.GetUser(ctx, tx, t.UserID)
		if err != nil {
			return err
		}
		if u.Status != "active" {
			return errors.New("owner not active")
		}
		return store.TouchAPIToken(ctx, tx, t.TokenID)
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}
