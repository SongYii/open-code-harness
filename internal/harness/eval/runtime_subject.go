package eval

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// resolveExecutionSubjects creates execution-only Subject copies after the
// frozen documents have passed ExpandAttempts. Overrides are restricted to
// plain HTTP loopback URLs for fixture Subjects; they never enter Attempt
// identity digests.
func resolveExecutionSubjects(subjects map[SubjectID]Subject, overrides map[SubjectID]string) (map[SubjectID]Subject, error) {
	resolved := make(map[SubjectID]Subject, len(subjects))
	for id, subject := range subjects {
		resolved[id] = subject
	}
	for id, endpoint := range overrides {
		subject, ok := subjects[id]
		if !ok {
			return nil, fmt.Errorf("provider endpoint override names unknown subject %q", id)
		}
		if subject.Provider.Lane != ProviderLaneFixture {
			return nil, fmt.Errorf("provider endpoint override for subject %q requires fixture lane", id)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.Opaque != "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !isLoopbackEndpointHost(parsed.Hostname()) {
			return nil, fmt.Errorf("provider endpoint override for subject %q must be a plain HTTP loopback URL without credentials, query, or fragment", id)
		}
		subject.Provider.NormalizedEndpoint = endpoint
		if err := subject.Validate(); err != nil {
			return nil, fmt.Errorf("provider endpoint override for subject %q: %w", id, err)
		}
		resolved[id] = subject
	}
	return resolved, nil
}

func isLoopbackEndpointHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
