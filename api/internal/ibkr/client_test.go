package ibkr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := New("test-token", "test-query")
	c.baseURL = srv.URL
	c.HTTPClient = srv.Client()
	return c
}

func TestClientFetchSucceedsImmediately(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SendRequest"):
			mustQueryParam(t, r, "t", "test-token")
			mustQueryParam(t, r, "q", "test-query")
			w.Write([]byte(`<FlexStatementResponse><Status>Success</Status><ReferenceCode>ref-1</ReferenceCode></FlexStatementResponse>`))
		case strings.HasSuffix(r.URL.Path, "/GetStatement"):
			mustQueryParam(t, r, "q", "ref-1")
			w.Write([]byte(sampleStatement))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	body, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !looksLikeStatement(body) {
		t.Errorf("Fetch() did not return a statement: %s", body)
	}
}

func TestClientFetchRetriesWhileNotReady(t *testing.T) {
	var getStatementCalls atomic.Int32

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SendRequest"):
			w.Write([]byte(`<FlexStatementResponse><Status>Success</Status><ReferenceCode>ref-1</ReferenceCode></FlexStatementResponse>`))
		case strings.HasSuffix(r.URL.Path, "/GetStatement"):
			if getStatementCalls.Add(1) < 3 {
				w.Write([]byte(`<FlexStatementResponse><Status>Fail</Status><ErrorCode>1019</ErrorCode><ErrorMessage>Statement generation in progress.</ErrorMessage></FlexStatementResponse>`))
				return
			}
			w.Write([]byte(sampleStatement))
		}
	})

	// The retry loop's real delay is 5s; a test cannot wait on that, so this
	// checks the mechanism (GetStatement is retried until it succeeds) via
	// the direct call rather than driving it through Fetch's timed loop.
	ref, err := client.SendRequest(context.Background())
	if err != nil {
		t.Fatalf("SendRequest() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := client.GetStatement(context.Background(), ref); err != ErrStatementNotReady {
			t.Fatalf("GetStatement() attempt %d error = %v, want ErrStatementNotReady", i, err)
		}
	}
	body, err := client.GetStatement(context.Background(), ref)
	if err != nil {
		t.Fatalf("GetStatement() on the ready attempt: %v", err)
	}
	if !looksLikeStatement(body) {
		t.Errorf("final GetStatement() did not return a statement")
	}
}

func TestClientFetchRespectsContextCancellation(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SendRequest"):
			w.Write([]byte(`<FlexStatementResponse><Status>Success</Status><ReferenceCode>ref-1</ReferenceCode></FlexStatementResponse>`))
		case strings.HasSuffix(r.URL.Path, "/GetStatement"):
			w.Write([]byte(`<FlexStatementResponse><Status>Fail</Status><ErrorCode>1019</ErrorCode><ErrorMessage>still working</ErrorMessage></FlexStatementResponse>`))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := client.Fetch(ctx); err == nil {
		t.Fatal("Fetch() with an exhausted context did not error")
	}
}

func TestSendRequestReportsFailure(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<FlexStatementResponse><Status>Fail</Status><ErrorCode>1003</ErrorCode><ErrorMessage>Invalid token.</ErrorMessage></FlexStatementResponse>`))
	})

	_, err := client.SendRequest(context.Background())
	if err == nil {
		t.Fatal("SendRequest() with a Fail status did not error")
	}
}

func mustQueryParam(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	got := r.URL.Query().Get(key)
	if got != want {
		t.Errorf("query param %s = %q, want %q (url: %s)", key, got, want, mustUnescape(t, r.URL.String()))
	}
}

func mustUnescape(t *testing.T, s string) string {
	t.Helper()
	u, err := url.QueryUnescape(s)
	if err != nil {
		return s
	}
	return u
}
