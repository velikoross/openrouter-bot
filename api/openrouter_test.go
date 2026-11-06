package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer key" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"z/paid","pricing":{"prompt":"0.000001","completion":"0.000002"}},
			{"id":"a/free:free","pricing":{"prompt":"0","completion":"0"}},
			{"id":"local"}]}`))
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL+"/", "key").ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || models[0].ID != "a/free:free" {
		t.Fatalf("models = %+v", models)
	}
	if !models[0].IsFree() || models[2].IsFree() || models[1].IsFree() {
		t.Error("IsFree")
	}
	if p := models[2].PricePerMillion(); p != "$1/$2" {
		t.Errorf("price = %q", p)
	}
	if models[0].PricePerMillion() != "free" || models[1].PricePerMillion() != "" {
		t.Error("price formatting")
	}
}

func TestListModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "").ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestGenerationCostRetriesUntilAvailable(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "gen-1" {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if atomic.AddInt32(&calls, 1) < 3 {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"total_cost":0.0123}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k")
	c.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	cost, err := c.GenerationCost(context.Background(), "gen-1")
	if err != nil || cost != 0.0123 || calls != 3 {
		t.Errorf("cost=%v err=%v calls=%d", cost, err, calls)
	}

	// Non-retryable error stops immediately.
	atomic.StoreInt32(&calls, 0)
	if _, err := c.GenerationCost(context.Background(), "other"); err == nil {
		t.Error("expected error")
	}
	if calls != 0 {
		t.Errorf("400 must not be retried, calls=%d", calls)
	}
}
