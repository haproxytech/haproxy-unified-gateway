// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package filters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ---------------------------------------------------------------------------
// originToPattern
// ---------------------------------------------------------------------------

func TestOriginToPattern(t *testing.T) {
	tests := []struct {
		origin  string
		pattern string
	}{
		{"https://example.com", `https://example\.com`},
		{"https://*.example.com", `https://.*\.example\.com`},
		{"*", `.*`},
		{"http://foo.bar.baz", `http://foo\.bar\.baz`},
		{"https://example.com:8080", `https://example\.com:8080`},
	}
	for _, tc := range tests {
		t.Run(tc.origin, func(t *testing.T) {
			assert.Equal(t, tc.pattern, originToPattern(tc.origin))
		})
	}
}

// ---------------------------------------------------------------------------
// buildCORSOriginConfig
// ---------------------------------------------------------------------------

func TestBuildCORSOriginConfig(t *testing.T) {
	tests := []struct {
		name             string
		origins          []gatewayv1.CORSOrigin
		allowCredentials bool
		wantHeader       string
		wantCondEmpty    bool // true when condTest should be ""
	}{
		{
			name:          "no origins → wildcard, no credentials",
			origins:       nil,
			wantHeader:    "*",
			wantCondEmpty: true,
		},
		{
			name:          "explicit wildcard, no credentials",
			origins:       []gatewayv1.CORSOrigin{"*"},
			wantHeader:    "*",
			wantCondEmpty: true,
		},
		{
			name:             "wildcard with credentials → echo origin",
			origins:          []gatewayv1.CORSOrigin{"*"},
			allowCredentials: true,
			wantHeader:       "%[req.hdr(Origin)]",
			wantCondEmpty:    false,
		},
		{
			name:          "specific origin",
			origins:       []gatewayv1.CORSOrigin{"https://example.com"},
			wantHeader:    "%[req.hdr(Origin)]",
			wantCondEmpty: false,
		},
		{
			name:          "multiple specific origins",
			origins:       []gatewayv1.CORSOrigin{"https://a.com", "https://b.com"},
			wantHeader:    "%[req.hdr(Origin)]",
			wantCondEmpty: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := buildCORSOriginConfig(tc.origins, tc.allowCredentials)
			assert.Equal(t, tc.wantHeader, cfg.headerValue)
			if tc.wantCondEmpty {
				assert.Empty(t, cfg.condTest)
			} else {
				assert.NotEmpty(t, cfg.condTest)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// quoteFmt
// ---------------------------------------------------------------------------

func TestQuoteFmt(t *testing.T) {
	assert.Equal(t, "GET", quoteFmt("GET"))
	assert.Equal(t, `"GET, POST"`, quoteFmt("GET, POST"))
	assert.Equal(t, `"a b"`, quoteFmt("a b"))
	assert.Equal(t, "nospace", quoteFmt("nospace"))
}

// ---------------------------------------------------------------------------
// regexEscapePath
// ---------------------------------------------------------------------------

func TestRegexEscapePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/api/v1", `/api/v1`},
		{"/api.v1", `/api\.v1`},
		{"/api+v1", `/api\+v1`},
		{"/foo/bar", `/foo/bar`},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, regexEscapePath(tc.input))
		})
	}
}

// ---------------------------------------------------------------------------
// requestRedirectRules
// ---------------------------------------------------------------------------

func TestRequestRedirectRules(t *testing.T) {
	tests := []struct {
		name        string
		filter      gatewayv1.HTTPRequestRedirectFilter
		matchPrefix string
		wantLen     int
		wantValue   string // RedirValue of the first rule
		wantType    string // RedirType
	}{
		{
			name: "scheme-only redirect",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Scheme: new("https"),
			},
			wantLen:   1,
			wantType:  "scheme",
			wantValue: "https",
		},
		{
			name: "hostname redirect",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Hostname: (*gatewayv1.PreciseHostname)(new("new.example.com")),
			},
			wantLen:   1,
			wantType:  "location",
			wantValue: "new.example.com%[path]",
		},
		{
			name: "full path replacement",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:            gatewayv1.FullPathHTTPPathModifier,
					ReplaceFullPath: new("/new-path"),
				},
			},
			wantLen:   1,
			wantType:  "location",
			wantValue: "/new-path",
		},
		{
			name: "prefix match on root",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/new"),
				},
			},
			matchPrefix: "/",
			wantLen:     1,
			wantType:    "location",
		},
		{
			name: "prefix match on non-root",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/new"),
				},
			},
			matchPrefix: "/old",
			wantLen:     1,
			wantType:    "location",
			wantValue:   "%[path,regsub(^/old,/new)]",
		},
		{
			// replacePrefixMatch "/" with root matchPrefix keeps the path unchanged.
			name: "replace root prefix with root preserves path",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/"),
				},
			},
			matchPrefix: "/",
			wantLen:     1,
			wantType:    "location",
			wantValue:   "%[path]",
		},
		{
			name: "custom status code",
			filter: gatewayv1.HTTPRequestRedirectFilter{
				Scheme:     new("https"),
				StatusCode: new(301),
			},
			wantLen:  1,
			wantType: "scheme",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := requestRedirectRules(&tc.filter, tc.matchPrefix)
			require.Len(t, rules, tc.wantLen)
			assert.Equal(t, tc.wantType, rules[0].RedirType)
			if tc.wantValue != "" {
				assert.Equal(t, tc.wantValue, rules[0].RedirValue)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// urlRewriteRules
// ---------------------------------------------------------------------------

func TestURLRewriteRules(t *testing.T) {
	tests := []struct {
		name        string
		filter      gatewayv1.HTTPURLRewriteFilter
		matchPrefix string
		wantLen     int
		wantType    string // Type of first rule
	}{
		{
			name: "hostname rewrite",
			filter: gatewayv1.HTTPURLRewriteFilter{
				Hostname: (*gatewayv1.PreciseHostname)(new("new.example.com")),
			},
			wantLen:  1,
			wantType: "set-header",
		},
		{
			name: "full path replacement",
			filter: gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:            gatewayv1.FullPathHTTPPathModifier,
					ReplaceFullPath: new("/new"),
				},
			},
			wantLen:  1,
			wantType: "set-path",
		},
		{
			name: "prefix match on root",
			filter: gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/new"),
				},
			},
			matchPrefix: "/",
			wantLen:     1,
			wantType:    "set-path",
		},
		{
			name: "prefix match on non-root",
			filter: gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/new"),
				},
			},
			matchPrefix: "/old",
			wantLen:     1,
			wantType:    "replace-path",
		},
		{
			// replacePrefixMatch "/" on a non-root prefix needs two rules:
			// one for the exact prefix (→ "/") and one for prefix+suffix.
			name: "strip prefix to root",
			filter: gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/"),
				},
			},
			matchPrefix: "/strip-prefix",
			wantLen:     2,
			wantType:    "replace-path",
		},
		{
			// replacePrefixMatch "/" with root matchPrefix is a no-op: every path
			// maps to itself, so no rewrite rule should be emitted.
			name: "replace root prefix with root is no-op",
			filter: gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: new("/"),
				},
			},
			matchPrefix: "/",
			wantLen:     0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := urlRewriteRules(&tc.filter, tc.matchPrefix)
			require.Len(t, rules, tc.wantLen)
			if len(rules) > 0 {
				assert.Equal(t, tc.wantType, rules[0].Type)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ToHAProxyRules — integration of filter pipeline
// ---------------------------------------------------------------------------

func TestToHAProxyRules(t *testing.T) {
	t.Run("request header modifier", func(t *testing.T) {
		result := ToHAProxyRules([]gatewayv1.HTTPRouteFilter{
			{
				Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
					Set:    []gatewayv1.HTTPHeader{{Name: "X-Foo", Value: "bar"}},
					Add:    []gatewayv1.HTTPHeader{{Name: "X-Added", Value: "yes"}},
					Remove: []string{"X-Remove"},
				},
			},
		}, "")
		assert.Len(t, result.HTTPRequestRules, 3)
		assert.Empty(t, result.HTTPResponseRules)
		assert.False(t, result.IsRedirect)
	})

	t.Run("response header modifier", func(t *testing.T) {
		result := ToHAProxyRules([]gatewayv1.HTTPRouteFilter{
			{
				Type: gatewayv1.HTTPRouteFilterResponseHeaderModifier,
				ResponseHeaderModifier: &gatewayv1.HTTPHeaderFilter{
					Set: []gatewayv1.HTTPHeader{{Name: "X-Resp", Value: "ok"}},
				},
			},
		}, "")
		assert.Empty(t, result.HTTPRequestRules)
		assert.Len(t, result.HTTPResponseRules, 1)
	})

	t.Run("request redirect sets IsRedirect", func(t *testing.T) {
		result := ToHAProxyRules([]gatewayv1.HTTPRouteFilter{
			{
				Type: gatewayv1.HTTPRouteFilterRequestRedirect,
				RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
					Scheme: new("https"),
				},
			},
		}, "")
		assert.True(t, result.IsRedirect)
		assert.NotEmpty(t, result.RedirectRules)
	})

	// Not yet implemented
	// t.Run("RequestMirror is silently ignored", func(t *testing.T) {
	// 	result := ToHAProxyRules([]gatewayv1.HTTPRouteFilter{
	// 		{Type: gatewayv1.HTTPRouteFilterRequestMirror},
	// 	}, "")
	// 	assert.Empty(t, result.HTTPRequestRules)
	// 	assert.Empty(t, result.HTTPResponseRules)
	// 	assert.False(t, result.IsRedirect)
	// })
}
