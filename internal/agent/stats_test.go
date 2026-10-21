package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

func TestStatsPollerConcurrentConfigure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := newStatsPoller("", u.Host)
	yamlText := fmt.Sprintf("trafficStats:\n  listen: %q\n  secret: test\n", u.Host)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p.Configure(yamlText)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = p.Poll(context.Background())
			}
		}()
	}
	wg.Wait()
	tx, rx := p.Totals()
	if tx != 0 || rx != 0 {
		t.Fatalf("totals = %d/%d", tx, rx)
	}
}
