package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidate200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/authentication" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	c := NewClient("validkey")
	c.baseURL = srv.URL

	if err := c.Validate(context.Background()); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidate401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"success":false}`))
	}))
	defer srv.Close()

	c := NewClient("badkey")
	c.baseURL = srv.URL

	err := c.Validate(context.Background())
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
}

func TestValidate500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient("anykey")
	c.baseURL = srv.URL

	err := c.Validate(context.Background())
	if err == nil {
		t.Fatal("Validate() = nil, want error for 500")
	}
}
