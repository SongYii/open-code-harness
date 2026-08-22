package docsguard_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// checkExternalCitationsEnv opts a run in to network access. Architecture
// gates cite repositories that move: badlogic/pi-mono, cited by three earlier
// gates, now redirects to earendil-works/pi. A citation that no longer
// resolves is evidence that rotted, but the network is not a dependency the
// pull-request path may take, so this gate runs only where it is asked for.
const checkExternalCitationsEnv = "DOCSGUARD_CHECK_EXTERNAL_LINKS"

var externalLinkPattern = regexp.MustCompile("https?://[^\\s)\\]\"'>`，。；：（）]+")

// externalCitations returns every distinct external URL in the documentation,
// with the documents that cite it.
func externalCitations(t *testing.T, root string) map[string][]string {
	t.Helper()
	citations := map[string][]string{}
	for _, document := range markdownFiles(t, root) {
		rel, err := filepath.Rel(root, document)
		if err != nil {
			rel = document
		}
		for _, raw := range externalLinkPattern.FindAllString(read(t, document), -1) {
			// Markdown commonly ends a sentence right after a link.
			target := strings.TrimRight(raw, ".,;:")
			if isNonRoutableExample(target) {
				continue
			}
			if !contains(citations[target], rel) {
				citations[target] = append(citations[target], rel)
			}
		}
	}
	return citations
}

// isNonRoutableExample reports whether a URL is an illustration rather than a
// citation. The provider design names http://169.254.169.254 as an address
// the adapter must never be allowed to reach; probing it would test this
// runner's own cloud metadata service, not a source. Loopback and private
// ranges appear the same way, in configuration examples.
func isNonRoutableExample(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	if address == nil {
		return false
	}
	return address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsUnspecified()
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// knownUnreachable records citations that no longer resolve, so the gate can
// stay green without the rot being swept away. Each entry names what was
// found and when, exactly as the evidence-ledger exemption does. An entry is
// a standing invitation to repair the citation, not a permanent excuse.
//
// Documentation rule 8 exists because of these two: both were cited by
// mutable path rather than by commit, so nothing pinned what was read.
var knownUnreachable = map[string]string{
	"https://docs.kurrent.io/getting-started/use-cases/time-travel/tutorial-3":                    "2026-08-19: redirects to /dev-center/use-cases/time-travel/tutorial-3, which also returns 404; cited by the 2026-08-13 runtime persistence gate",
	"https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/session.sql.ts": "2026-08-19: returns 404, and also returns 404 at the repository's 2026-08-13 default-branch commit 8a55ba7; a code search of the repository finds no file by that name. Cited by the 2026-08-13 EventStore v2 and runtime persistence gates; the observation behind it needs re-derivation from a pinned source",
}

type citationResult struct {
	url      string
	status   int
	finalURL string
	err      error
}

// TestExternalCitationsResolve reports citations that no longer resolve or
// that now redirect elsewhere.
//
// A redirect is reported rather than failed: a repository move does not
// invalidate the observation a gate recorded, it only means later gates must
// cite the new location. A dead link is a failure, because a citation nobody
// can follow is not evidence.
func TestExternalCitationsResolve(t *testing.T) {
	if os.Getenv(checkExternalCitationsEnv) == "" {
		t.Skipf("set %s=1 to check external citations over the network", checkExternalCitationsEnv)
	}
	root := repoRoot(t)
	citations := externalCitations(t, root)
	for url := range citations {
		if _, ok := knownUnreachable[url]; ok {
			delete(citations, url)
		}
	}
	urls := make([]string, 0, len(citations))
	for url := range citations {
		urls = append(urls, url)
	}
	sort.Strings(urls)
	if len(urls) == 0 {
		t.Fatal("no external citations found; this gate is no longer reading the documents")
	}

	results := make([]citationResult, len(urls))
	var group sync.WaitGroup
	// Four at a time keeps a documentation sweep from looking like abuse to
	// the hosts being cited.
	tokens := make(chan struct{}, 4)
	for index, url := range urls {
		group.Add(1)
		go func(index int, url string) {
			defer group.Done()
			tokens <- struct{}{}
			defer func() { <-tokens }()
			results[index] = probeCitation(url)
		}(index, url)
	}
	group.Wait()

	for url, reason := range knownUnreachable {
		t.Logf("citation %s is a recorded dead link: %s", url, reason)
	}
	for _, result := range results {
		where := strings.Join(citations[result.url], ", ")
		switch {
		case result.err != nil:
			t.Errorf("citation %s (cited by %s) could not be reached: %v", result.url, where, result.err)
		case result.status >= 400:
			t.Errorf("citation %s (cited by %s) returned HTTP %d", result.url, where, result.status)
		case result.finalURL != "" && result.finalURL != result.url:
			t.Logf("citation %s (cited by %s) now redirects to %s; later gates must cite the new location",
				result.url, where, result.finalURL)
		}
	}
}

func probeCitation(url string) citationResult {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return citationResult{url: url, err: err}
	}
	// Some hosts serve 403 to a client that presents no user agent.
	request.Header.Set("User-Agent", "open-code-harness-docsguard/1")
	response, err := client.Do(request)
	if err != nil {
		return citationResult{url: url, err: err}
	}
	defer func() { _ = response.Body.Close() }()
	final := ""
	if response.Request != nil && response.Request.URL != nil {
		final = response.Request.URL.String()
	}
	return citationResult{url: url, status: response.StatusCode, finalURL: final}
}

func init() {
	if _, err := os.Stat("citations_test.go"); err != nil {
		panic(fmt.Sprintf("docsguard gates must run from their own directory: %v", err))
	}
}
