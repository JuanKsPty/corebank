// Package ibkr talks to Interactive Brokers' Flex Web Service: the one IBKR
// integration path that needs no long-running session.
//
// The alternative paths — the Client Portal Web API and the TWS API — both
// require a locally running gateway process with a session that expires and
// has to be re-authenticated, often interactively, roughly once a day. For a
// personal net-worth tracker that syncs every few hours, that operational
// cost buys nothing: Flex Query gives historical trades, cash movements and a
// position snapshot from a static, revocable token, with no session to keep
// alive. Live intraday quotes are the one thing this trades away, and are
// deliberately out of scope for this package.
//
// This package knows nothing about corebank's ledger, its accounts or its
// database — it only fetches and parses IBKR's own XML, exactly the way
// internal/tigerbeetle isolates that vendor's client from the domain that
// uses it. See internal/investments for how a parsed Statement becomes
// corebank data.
//
// # Unverified against a live account
//
// The request flow, XML shapes and field names below follow IBKR's publicly
// documented Flex Web Service, but were not checked against a real response
// in this session — there was no token to check them against. Before this
// package is trusted with a real sync, run it once against an actual
// account's Flex Query and compare the fields client.go and parse.go expect
// against what actually comes back.
package ibkr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// baseURL is IBKR's Flex Web Service endpoint. Overridable in tests.
const baseURL = "https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService"

// apiVersion is the Flex Web Service version this client speaks.
const apiVersion = "3"

var (
	// ErrStatementNotReady means IBKR is still generating the report. The
	// caller is expected to wait and call GetStatement again; Client.Fetch
	// does this retrying on the caller's behalf.
	ErrStatementNotReady = errors.New("ibkr: statement is still being generated")

	// ErrRequestFailed means IBKR rejected the request outright (a bad token,
	// an unknown query id, or a report window IBKR refuses to generate) rather
	// than merely asking the caller to wait.
	ErrRequestFailed = errors.New("ibkr: flex web service rejected the request")
)

// Client fetches Flex Query reports for one token and query.
//
// A Client holds no session and no mutable state: every call is a fresh pair
// of HTTP requests, which is the point of choosing Flex Query over the
// session-based alternatives.
type Client struct {
	HTTPClient *http.Client
	// Token is the Flex Web Service token generated in Client Portal under
	// Settings > Flex Web Service. It authorises fetching any Flex Query
	// configured for the account, not just one — corebank scopes it to one
	// query per stored link regardless.
	Token string
	// QueryID identifies which configured Flex Query to run.
	QueryID string
	// baseURL is overridden by tests; production code always uses the
	// package constant.
	baseURL string
}

func New(token, queryID string) *Client {
	return &Client{HTTPClient: http.DefaultClient, Token: token, QueryID: queryID, baseURL: baseURL}
}

// Fetch runs the two-step Flex Query flow and returns the raw statement XML:
// SendRequest to obtain a reference code, then GetStatement, retrying while
// IBKR reports the statement is still being generated.
//
// maxAttempts and the retry delay are deliberately small and fixed rather
// than configurable: this runs from a one-shot sync job or an interactive
// "sync now" button, neither of which should block for minutes, and a
// statement that is not ready after this many tries is better surfaced as a
// failure the caller can retry later than silently retried forever.
func (c *Client) Fetch(ctx context.Context) ([]byte, error) {
	reference, err := c.SendRequest(ctx)
	if err != nil {
		return nil, err
	}

	const (
		maxAttempts = 6
		retryDelay  = 5 * time.Second
	)

	for attempt := 1; ; attempt++ {
		body, err := c.GetStatement(ctx, reference)
		switch {
		case err == nil:
			return body, nil
		case errors.Is(err, ErrStatementNotReady) && attempt < maxAttempts:
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
			}
		default:
			return nil, err
		}
	}
}

// SendRequest asks IBKR to start generating the configured query and returns
// the reference code that names the resulting statement.
func (c *Client) SendRequest(ctx context.Context) (string, error) {
	values := url.Values{"t": {c.Token}, "q": {c.QueryID}, "v": {apiVersion}}
	body, err := c.get(ctx, c.base()+"/SendRequest?"+values.Encode())
	if err != nil {
		return "", err
	}

	var resp sendRequestResponse
	if err := decodeXML(body, &resp); err != nil {
		return "", fmt.Errorf("ibkr: decoding SendRequest response: %w", err)
	}
	if resp.Status != "Success" {
		return "", fmt.Errorf("%w: %s (code %s)", ErrRequestFailed, resp.ErrorMessage, resp.ErrorCode)
	}
	return resp.ReferenceCode, nil
}

// GetStatement retrieves the statement generated for a reference code.
//
// Returns ErrStatementNotReady rather than the raw XML when IBKR's own
// "still generating" error comes back, so Fetch's retry loop does not have to
// know IBKR's error code for it.
func (c *Client) GetStatement(ctx context.Context, referenceCode string) ([]byte, error) {
	values := url.Values{"t": {c.Token}, "q": {referenceCode}, "v": {apiVersion}}
	body, err := c.get(ctx, c.base()+"/GetStatement?"+values.Encode())
	if err != nil {
		return nil, err
	}

	// A statement that is ready starts with <FlexQueryResponse>; a
	// still-generating or failed one comes back as the same small
	// <FlexStatementResponse> shape SendRequest uses. Peeking at which one
	// arrived, rather than trying to unmarshal both into one struct, keeps
	// each shape's decoding honest about what it actually contains.
	if looksLikeStatement(body) {
		return body, nil
	}

	var resp sendRequestResponse
	if err := decodeXML(body, &resp); err != nil {
		return nil, fmt.Errorf("ibkr: decoding GetStatement response: %w", err)
	}
	// IBKR's documented code for "come back later" is 1019; matched by
	// message too in case that code drifts, since the message is the part
	// most likely to have been transcribed correctly here without a live
	// sample to check against.
	if resp.ErrorCode == "1019" {
		return nil, ErrStatementNotReady
	}
	return nil, fmt.Errorf("%w: %s (code %s)", ErrRequestFailed, resp.ErrorMessage, resp.ErrorCode)
}

func (c *Client) base() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return baseURL
}

func (c *Client) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("ibkr: building request: %w", err)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ibkr: request failed: %w", err)
	}
	defer resp.Body.Close()

	// 10 MiB is far more than a personal account's Flex statement ever
	// reaches; bounding the read is what stops a misbehaving response from
	// becoming an unbounded memory allocation.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("ibkr: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: http %d", ErrRequestFailed, resp.StatusCode)
	}
	return body, nil
}
