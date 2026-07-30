package objects

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The refusals below need no server: they are the adapter's two structural
// guarantees — that nothing here can delete or abort something the caller
// did not name — and both must hold before a request is ever built.

func TestNewRequiresEndpointAndBucket(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no endpoint": {Bucket: "maestro"},
		"no bucket":   {Endpoint: "http://127.0.0.1:59000"},
		"neither":     {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("expected a rejection, got a client")
			}
		})
	}
}

// TestNewStripsEndpointScheme guards the seam between the bootstrap
// pointer, which stores a URL, and the client, which takes a host:port and
// would otherwise treat "http://127.0.0.1" as a hostname.
func TestNewStripsEndpointScheme(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:59000",
		"https://127.0.0.1:59000",
		"127.0.0.1:59000",
	} {
		blob, err := New(Config{Endpoint: endpoint, Bucket: "maestro"})
		if err != nil {
			t.Fatalf("New(%q): %v", endpoint, err)
		}
		if host := blob.core.EndpointURL().Host; host != "127.0.0.1:59000" {
			t.Fatalf("New(%q) resolved host %q, want 127.0.0.1:59000", endpoint, host)
		}
	}
}

func TestDeleteVersionRefusesAnUnnamedVersion(t *testing.T) {
	blob, err := New(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A live endpoint is not needed: the refusal must precede the request,
	// so reaching the server at all would itself be the failure.
	err = blob.DeleteVersion(context.Background(), "org/aa/bb/digest", "")
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "delete marker") {
		t.Fatalf("refusal does not explain the delete-marker hazard: %v", err)
	}
}

func TestAbortUploadRefusesAnUnnamedUpload(t *testing.T) {
	blob, err := New(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = blob.AbortUpload(context.Background(), "org/aa/bb/digest", "")
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "concurrent writer") {
		t.Fatalf("refusal does not explain the concurrent-writer hazard: %v", err)
	}
}

// The two tests below drive the multipart listing against CANNED
// responses, and they are unit tests on purpose.
//
// Neither behaviour they protect can be produced by the pinned server. It
// ignores `max-uploads` entirely — it answers with everything it has and
// never sets IsTruncated, so the paging loop is unreachable against it —
// and it treats the listing prefix as an exact object key, so the
// exact-key filter never has anything to narrow. Both mutations survive
// every real-server test in this package, which is precisely the
// "guard behind a working guard" case: unexercisable here, and the only
// thing standing between a portable adapter and a silently wrong one on a
// store that follows the protocol.
//
// So the server is stubbed to behave the way the protocol says, and what
// is asserted is what the ADAPTER does with it — including the exact
// markers it sends on the second request, which is the whole of the fix.

// uploadListing serves a scripted sequence of multipart listings and
// records the query each request carried.
type uploadListing struct {
	pages    []string
	requests []url.Values
}

func (u *uploadListing) RoundTrip(req *http.Request) (*http.Response, error) {
	// The client resolves the bucket's region before its first call and
	// caches it. Answering here keeps the test off the network entirely.
	if req.URL.Query().Has("location") {
		return xmlResponse(req, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
			`us-east-1</LocationConstraint>`), nil
	}
	if !req.URL.Query().Has("uploads") {
		return nil, fmt.Errorf("unexpected request to %s", req.URL)
	}
	u.requests = append(u.requests, req.URL.Query())
	if len(u.requests) > len(u.pages) {
		return nil, fmt.Errorf("the adapter asked for page %d of %d; it is not terminating",
			len(u.requests), len(u.pages))
	}
	return xmlResponse(req, u.pages[len(u.requests)-1]), nil
}

func xmlResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/xml"}},
		Request:    req,
	}
}

// uploadPage renders one ListMultipartUploads response.
func uploadPage(truncated bool, nextKey, nextUploadID string, uploads ...[2]string) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		`<Bucket>maestro</Bucket>`)
	fmt.Fprintf(&body, `<IsTruncated>%t</IsTruncated>`, truncated)
	fmt.Fprintf(&body, `<NextKeyMarker>%s</NextKeyMarker>`, nextKey)
	fmt.Fprintf(&body, `<NextUploadIdMarker>%s</NextUploadIdMarker>`, nextUploadID)
	for _, upload := range uploads {
		fmt.Fprintf(&body, `<Upload><Key>%s</Key><UploadId>%s</UploadId></Upload>`,
			upload[0], upload[1])
	}
	body.WriteString(`</ListMultipartUploadsResult>`)
	return body.String()
}

func stubbedBlob(t *testing.T, listing *uploadListing) *Blob {
	t.Helper()
	blob, err := newBlob(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"}, listing)
	if err != nil {
		t.Fatalf("newBlob: %v", err)
	}
	return blob
}

// TestListUploadsAdvancesBothPagingMarkers is the round-8 fix under test:
// several uploads in flight on ONE key page correctly only if the upload-id
// marker advances with the key marker. Paging on the key alone restarts at
// that key's first upload, so the listing repeats forever.
func TestListUploadsAdvancesBothPagingMarkers(t *testing.T) {
	const key = "org/aa/bb/contended"
	listing := &uploadListing{pages: []string{
		uploadPage(true, key, "upload-1", [2]string{key, "upload-1"}),
		uploadPage(true, key, "upload-2", [2]string{key, "upload-2"}),
		uploadPage(false, "", "", [2]string{"org/aa/cc/other", "upload-3"}),
	}}
	blob := stubbedBlob(t, listing)

	uploads, err := blob.ListUploadsUnder(context.Background(), "org/")
	if err != nil {
		t.Fatalf("ListUploadsUnder: %v", err)
	}
	if len(uploads) != 3 {
		t.Fatalf("paging returned %d uploads, want 3: %v", len(uploads), uploads)
	}
	if len(listing.requests) != 3 {
		t.Fatalf("the adapter made %d requests, want 3", len(listing.requests))
	}

	// The markers are the point. A key-only implementation sends the key
	// again with an empty upload-id marker and is served the same page.
	for page, want := range []struct{ keyMarker, uploadIDMarker string }{
		{"", ""},
		{key, "upload-1"},
		{key, "upload-2"},
	} {
		got := listing.requests[page]
		if got.Get("key-marker") != want.keyMarker || got.Get("upload-id-marker") != want.uploadIDMarker {
			t.Fatalf("request %d carried key-marker=%q upload-id-marker=%q, want %q and %q",
				page, got.Get("key-marker"), got.Get("upload-id-marker"),
				want.keyMarker, want.uploadIDMarker)
		}
	}
}

// TestListUploadsForKeyIgnoresKeysItMerelyPrefixes protects the exact-key
// filter against a store with true S3 prefix semantics, which answers this
// listing with every key the given one is a prefix of. Without the filter,
// staging cleanup would abort a different writer's upload — the unfenced
// abort the whole module is built to refuse.
func TestListUploadsForKeyIgnoresKeysItMerelyPrefixes(t *testing.T) {
	const key = "staging/org/upload"
	listing := &uploadListing{pages: []string{
		uploadPage(false, "", "",
			[2]string{key, "mine"},
			[2]string{key + "-other", "someone-elses"},
			[2]string{key + "/nested", "also-not-mine"},
		),
	}}
	blob := stubbedBlob(t, listing)

	uploads, err := blob.ListUploadsForKey(context.Background(), key)
	if err != nil {
		t.Fatalf("ListUploadsForKey: %v", err)
	}
	if len(uploads) != 1 || uploads[0].UploadID != "mine" {
		t.Fatalf("ListUploadsForKey(%s) returned %v, want only this key's upload", key, uploads)
	}
}

// TestListUploadsUnderFiltersByPrefix is the same guard on the other
// operation. It asks the server for everything — the only listing this
// server answers — so the prefix is applied here or not at all.
func TestListUploadsUnderFiltersByPrefix(t *testing.T) {
	listing := &uploadListing{pages: []string{
		uploadPage(false, "", "",
			[2]string{"staging/org/one", "wanted"},
			[2]string{"other-org/aa/bb/digest", "elsewhere"},
		),
	}}
	blob := stubbedBlob(t, listing)

	uploads, err := blob.ListUploadsUnder(context.Background(), "staging/")
	if err != nil {
		t.Fatalf("ListUploadsUnder: %v", err)
	}
	if len(uploads) != 1 || uploads[0].UploadID != "wanted" {
		t.Fatalf("ListUploadsUnder returned %v, want only the staging upload", uploads)
	}
	// The request must not carry the prefix: this server answers a
	// prefixed listing with nothing at all.
	if got := listing.requests[0].Get("prefix"); got != "" {
		t.Fatalf("the listing request carried prefix=%q; on this server that returns nothing", got)
	}
}

func TestListUploadsForKeyRefusesAnEmptyKey(t *testing.T) {
	blob, err := New(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := blob.ListUploadsForKey(context.Background(), ""); err == nil {
		t.Fatal("expected a refusal: an empty key enumerates the whole bucket")
	}
}

// errorResponse serves one canned S3 error to every request except the
// bucket-region lookup.
type errorResponse struct {
	code   string
	status int
}

func (e errorResponse) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Query().Has("location") {
		return xmlResponse(req, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
			`us-east-1</LocationConstraint>`), nil
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code>`+
		`<Message>canned</Message></Error>`, e.code)
	response := xmlResponse(req, body)
	response.StatusCode = e.status
	return response, nil
}

// TestAbortUploadToleratesAnAlreadyGoneUpload protects the reconciler's
// retry against a store that follows S3.
//
// The pinned server answers a repeat abort with no error at all, so the
// integration suite cannot tell whether this tolerance exists — it passes
// either way. S3 answers NoSuchUpload, and there the tolerance is the
// difference between a claim that clears and one that is retried forever.
func TestAbortUploadToleratesAnAlreadyGoneUpload(t *testing.T) {
	blob, err := newBlob(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"},
		errorResponse{code: "NoSuchUpload", status: http.StatusNotFound})
	if err != nil {
		t.Fatalf("newBlob: %v", err)
	}
	if err := blob.AbortUpload(context.Background(), "staging/org/gone", "upload-1"); err != nil {
		t.Fatalf("abort of an already-gone upload: %v", err)
	}
}

// TestDeleteVersionToleratesAnAlreadyGoneVersion is the same protection for
// the other half of a claim, and it is the case the claim is FOR: a crash
// after the remote delete and before the row clears leaves the reconciler to
// re-issue a delete for a version that is already gone.
//
// The pinned server answers that with no error at all -- measured, including
// for an unknown version id and for a key that never existed -- so the
// integration suite passes whether or not this tolerance exists. On a store
// that answers NoSuchVersion it is the difference between a claim that clears
// and one retried at every startup forever.
func TestDeleteVersionToleratesAnAlreadyGoneVersion(t *testing.T) {
	for _, code := range []string{"NoSuchVersion", "NoSuchKey"} {
		blob, err := newBlob(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"},
			errorResponse{code: code, status: http.StatusNotFound})
		if err != nil {
			t.Fatalf("newBlob: %v", err)
		}
		if err := blob.DeleteVersion(context.Background(), "org/aa/bb/gone", "version-1"); err != nil {
			t.Errorf("delete of an already-gone version answering %s: %v", code, err)
		}
	}
}

// TestDeleteVersionStillReportsOtherFailures is the other half, and it
// matters more here than anywhere: a swallowed failure would let the sweep
// clear a claim over storage that is still in the bucket, which is precisely
// the leak the claim was written to prevent.
func TestDeleteVersionStillReportsOtherFailures(t *testing.T) {
	blob, err := newBlob(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"},
		errorResponse{code: "AccessDenied", status: http.StatusForbidden})
	if err != nil {
		t.Fatalf("newBlob: %v", err)
	}
	if err := blob.DeleteVersion(context.Background(), "org/aa/bb/denied", "version-1"); err == nil {
		t.Fatal("DeleteVersion swallowed an access-denied response")
	}
}

// TestAbortUploadStillReportsOtherFailures is the other half: tolerating
// every error would turn a permissions failure or an unreachable server
// into a silent success, and the claim would be cleared over storage that
// was never reclaimed.
func TestAbortUploadStillReportsOtherFailures(t *testing.T) {
	blob, err := newBlob(Config{Endpoint: "127.0.0.1:59000", Bucket: "maestro"},
		errorResponse{code: "AccessDenied", status: http.StatusForbidden})
	if err != nil {
		t.Fatalf("newBlob: %v", err)
	}
	if err := blob.AbortUpload(context.Background(), "staging/org/denied", "upload-1"); err == nil {
		t.Fatal("AbortUpload swallowed an access-denied response")
	}
}

// TestFencedVersionRejectsAnUnusableID covers both halves of the fence a
// write depends on, one of which the pinned server cannot produce.
//
// It answers an unversioned or suspended write with an EMPTY id, so the
// integration suite exercises that arm and nothing else. A store following
// S3 reports the literal null version instead, and there the empty check
// alone would let an unfenced write through.
//
// A null version is perfectly deletable — TestListVersionsSeesTheNullVersion
// removes one by name, because the sweep has to reclaim objects that
// predate versioning. What it cannot do is FENCE: `null` is the slot every
// unversioned write to a key reuses, not one generation, so a delete
// condemning this object would remove whatever occupies the slot when it
// arrives.
//
// Tested here rather than through PutStaged because reaching it any other
// way means a server that reports what this one does not.
func TestFencedVersionRejectsAnUnusableID(t *testing.T) {
	const good = "4281afe4-9a7e-43ef-80aa-306635b5957f"
	for name, testCase := range map[string]struct {
		version  string
		accepted bool
	}{
		"a real version":      {good, true},
		"no version at all":   {"", false},
		"the null version":    {"null", false},
		"a version named nul": {"nul", true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := fencedVersion("org/aa/bb/digest", testCase.version)
			if testCase.accepted {
				if err != nil {
					t.Fatalf("fencedVersion(%q) refused a usable id: %v", testCase.version, err)
				}
				if got != testCase.version {
					t.Fatalf("fencedVersion(%q) returned %q", testCase.version, got)
				}
				return
			}
			if err == nil {
				t.Fatalf("fencedVersion(%q) accepted an id that cannot fence a later delete",
					testCase.version)
			}
			if got != "" {
				t.Fatalf("fencedVersion(%q) returned %q alongside its error", testCase.version, got)
			}
			if !strings.Contains(err.Error(), "not versioned") {
				t.Fatalf("the refusal does not name the cause: %v", err)
			}
		})
	}
}
