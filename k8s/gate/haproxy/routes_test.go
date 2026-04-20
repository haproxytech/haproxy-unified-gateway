package haproxy

import (
	"testing"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestSanitizeRegexp(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no dots",
			input:    "abc",
			expected: "abc",
		},
		{
			name:     "single dot",
			input:    "a.b",
			expected: "a\\.b",
		},
		{
			name:     "multiple dots",
			input:    "a.b.c",
			expected: "a\\.b\\.c",
		},
		{
			name:     "dot star",
			input:    "a.*b",
			expected: "a.*b",
		},
		{
			name:     "dot star and single dot",
			input:    "a.*b.c",
			expected: "a.*b\\.c",
		},
		{
			name:     "single dot and dot star",
			input:    "a.b.*c",
			expected: "a\\.b.*c",
		},
		{
			name:     "starts with dot",
			input:    ".abc",
			expected: "\\.abc",
		},
		{
			name:     "starts with dot star",
			input:    ".*abc",
			expected: ".*abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeRegexp(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeRegexp(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestResolveEntry covers the pathMatch × hostKind combinations and guards
// against regressions where wildcard-host + exact-path entries were silently
// dropped into a dead `domainWildcard` bucket that was never applied to any
// map file.
func TestResolveEntry(t *testing.T) {
	ptr := func(s gatewayv1.PathMatchType) *gatewayv1.PathMatchType { return &s }
	strPtr := func(s string) *string { return &s }

	type bucket int
	const (
		bucketExact bucket = iota
		bucketPrefix
		bucketRegex
	)

	tests := []struct {
		name       string
		hostname   string
		pathType   gatewayv1.PathMatchType
		path       string
		wantBucket bucket
		wantKey    maps.EntryKey
	}{
		{
			name:       "exact-path exact-host",
			hostname:   "example.com",
			pathType:   gatewayv1.PathMatchExact,
			path:       "/foo",
			wantBucket: bucketExact,
			wantKey:    maps.EntryKey{Hostname: "example.com", Path: "/foo"},
		},
		{
			name:       "exact-path wildcard-host falls through to exact bucket",
			hostname:   "*.example.com",
			pathType:   gatewayv1.PathMatchExact,
			path:       "/foo",
			wantBucket: bucketExact,
			wantKey:    maps.EntryKey{Hostname: ".example.com", Path: "/foo"},
		},
		{
			name:       "prefix-path exact-host",
			hostname:   "example.com",
			pathType:   gatewayv1.PathMatchPathPrefix,
			path:       "/foo",
			wantBucket: bucketPrefix,
			wantKey:    maps.EntryKey{Hostname: "example.com", Path: "/foo"},
		},
		{
			name:       "prefix-path wildcard-host",
			hostname:   "*.example.com",
			pathType:   gatewayv1.PathMatchPathPrefix,
			path:       "/foo",
			wantBucket: bucketRegex,
			wantKey:    maps.EntryKey{Hostname: `\.example\.com`, Path: "/foo.*"},
		},
		{
			name:       "regex-path exact-host",
			hostname:   "example.com",
			pathType:   gatewayv1.PathMatchRegularExpression,
			path:       "/foo",
			wantBucket: bucketRegex,
			wantKey:    maps.EntryKey{Hostname: `^example\.com`, Path: "/foo"},
		},
		{
			name:       "regex-path wildcard-host",
			hostname:   "*.example.com",
			pathType:   gatewayv1.PathMatchRegularExpression,
			path:       "/foo",
			wantBucket: bucketRegex,
			wantKey:    maps.EntryKey{Hostname: `\.example\.com`, Path: "/foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDesiredBackendsMaps()
			match := gatewayv1.HTTPRouteMatch{
				Path: &gatewayv1.HTTPPathMatch{
					Type:  ptr(tt.pathType),
					Value: strPtr(tt.path),
				},
			}
			innerMap, key := d.resolveEntry(tt.hostname, match)

			if key != tt.wantKey {
				t.Errorf("resolveEntry key = %+v, want %+v", key, tt.wantKey)
			}
			if innerMap == nil {
				t.Fatal("resolveEntry returned nil inner map")
			}

			buckets := map[bucket]map[maps.EntryKey]map[string]*maps.WeightedValue{
				bucketExact:  d.exact,
				bucketPrefix: d.prefix,
				bucketRegex:  d.regex,
			}
			for b, m := range buckets {
				_, present := m[tt.wantKey]
				if b == tt.wantBucket {
					if !present {
						t.Errorf("key %+v missing from expected bucket %d", tt.wantKey, b)
					}
				} else if present {
					t.Errorf("key %+v leaked into unexpected bucket %d", tt.wantKey, b)
				}
			}
		})
	}
}
