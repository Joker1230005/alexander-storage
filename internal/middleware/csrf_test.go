package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSRFMiddleware_GETRequestPassesThrough(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check token is in context
		token := TokenFromContext(r.Context())
		assert.NotEmpty(t, token)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Check CSRF cookie was set
	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "csrf_token" {
			csrfCookie = c
			break
		}
	}
	require.NotNil(t, csrfCookie)
	assert.NotEmpty(t, csrfCookie.Value)
}

func TestCSRFMiddleware_POSTWithoutTokenFails(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/dashboard/buckets/test/acl", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRFMiddleware_POSTWithValidTokenSucceeds(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First, get a token via GET request
	getReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	var token string
	for _, c := range getRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			token = c.Value
			break
		}
	}
	require.NotEmpty(t, token)

	// Now make POST with token in header
	postReq := httptest.NewRequest(http.MethodPost, "/dashboard/buckets/test/acl", nil)
	postReq.Header.Set("X-CSRF-Token", token)
	postReq.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	postRec := httptest.NewRecorder()

	handler.ServeHTTP(postRec, postReq)

	assert.Equal(t, http.StatusOK, postRec.Code)
}

func TestCSRFMiddleware_POSTWithFormTokenSucceeds(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First, get a token via GET request
	getReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	var token string
	for _, c := range getRec.Result().Cookies() {
		if c.Name == "csrf_token" {
			token = c.Value
			break
		}
	}
	require.NotEmpty(t, token)

	// Now make POST with token in form field
	form := url.Values{}
	form.Set("csrf_token", token)
	postReq := httptest.NewRequest(http.MethodPost, "/dashboard/buckets/test/acl", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	postRec := httptest.NewRecorder()

	handler.ServeHTTP(postRec, postReq)

	assert.Equal(t, http.StatusOK, postRec.Code)
}

func TestCSRFMiddleware_POSTWithInvalidTokenFails(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make POST with mismatched token
	postReq := httptest.NewRequest(http.MethodPost, "/dashboard/buckets/test/acl", nil)
	postReq.Header.Set("X-CSRF-Token", "wrong-token")
	postReq.AddCookie(&http.Cookie{Name: "csrf_token", Value: "correct-token"})
	postRec := httptest.NewRecorder()

	handler.ServeHTTP(postRec, postReq)

	assert.Equal(t, http.StatusForbidden, postRec.Code)
}

func TestCSRFMiddleware_ExemptPathBypassesValidation(t *testing.T) {
	config := DefaultCSRFConfig()
	config.ExemptPaths = []string{"/dashboard/login"}
	csrf := NewCSRFMiddleware(config)

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// POST to login without token should succeed (exempt path)
	postReq := httptest.NewRequest(http.MethodPost, "/dashboard/login", nil)
	postRec := httptest.NewRecorder()

	handler.ServeHTTP(postRec, postReq)

	assert.Equal(t, http.StatusOK, postRec.Code)
}

func TestCSRFMiddleware_HEADMethodExempt(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodHead, "/dashboard/buckets/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCSRFMiddleware_OPTIONSMethodExempt(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/dashboard/buckets/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCSRFMiddleware_DeleteRequiresToken(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// DELETE without token should fail
	req := httptest.NewRequest(http.MethodDelete, "/dashboard/users/1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRFMiddleware_PUTRequiresToken(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// PUT without token should fail
	req := httptest.NewRequest(http.MethodPut, "/dashboard/buckets/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRFMiddleware_ClearToken(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	rec := httptest.NewRecorder()
	csrf.ClearToken(rec)

	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "csrf_token" {
			csrfCookie = c
			break
		}
	}

	require.NotNil(t, csrfCookie)
	assert.Equal(t, "", csrfCookie.Value)
	assert.Equal(t, -1, csrfCookie.MaxAge) // Cookie deletion
}

func TestTokenFromContext_EmptyWhenMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	token := TokenFromContext(req.Context())
	assert.Empty(t, token)
}

func TestCSRFMiddleware_TokenInContextAfterGET(t *testing.T) {
	csrf := NewCSRFMiddleware(DefaultCSRFConfig())

	var contextToken string
	handler := csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextToken = TokenFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.NotEmpty(t, contextToken)

	// Token in context should match cookie
	var cookieToken string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf_token" {
			cookieToken = c.Value
			break
		}
	}
	assert.Equal(t, cookieToken, contextToken)
}
