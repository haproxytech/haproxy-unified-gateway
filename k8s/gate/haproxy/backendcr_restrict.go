// Copyright 2026 HAProxy Technologies LLC
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

package haproxy

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/go-openapi/strfmt"
	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
)

// allFields marks a Backend CR key whose whole value is kept.
var allFields map[string]any

// allowedBackendCRFields lists the Backend CR keys, as written in the CR, that
// a Backend CR from an untrusted namespace may set. A nested map restricts the
// sub-keys of that key. Everything else is dropped: rule lists, ACLs, error
// files, log targets, stick tables, external checks and any other directive
// that reads a file, evaluates an expression or touches shared state.
var allowedBackendCRFields = map[string]any{
	// Load balancing
	"balance": map[string]any{
		"algorithm": allFields, "hdr_name": allFields, "hdr_use_domain_only": allFields,
		"random_draws": allFields, "uri_depth": allFields, "uri_len": allFields,
		"uri_path_only": allFields, "uri_whole": allFields, "url_param": allFields,
		"url_param_check_post": allFields, "url_param_max_wait": allFields,
	},
	"hash_type":           allFields,
	"hash_balance_factor": allFields,
	// Timeouts
	"check_timeout":           allFields,
	"connect_timeout":         allFields,
	"queue_timeout":           allFields,
	"server_timeout":          allFields,
	"server_fin_timeout":      allFields,
	"tunnel_timeout":          allFields,
	"http_request_timeout":    allFields,
	"http_keep_alive_timeout": allFields,
	// Retries and queueing
	"retries":              allFields,
	"retry_on":             allFields,
	"redispatch":           allFields,
	"fullconn":             allFields,
	"max_keep_alive_queue": allFields,
	// Connection handling
	"abortonclose":           allFields,
	"http_connection_mode":   allFields,
	"http_reuse":             allFields,
	"http-buffer-request":    allFields,
	"http_pretend_keepalive": allFields,
	"prefer_last_server":     allFields,
	"forwardfor":             allFields,
	// Health checks
	"adv_check":         allFields,
	"httpchk_params":    allFields,
	"log_health_checks": allFields,
	// Persistence
	"cookie": allFields,
	// Server defaults
	"default_server": map[string]any{
		"check": allFields, "inter": allFields, "fastinter": allFields, "downinter": allFields,
		"fall": allFields, "rise": allFields, "health_check_port": allFields,
		"maxconn": allFields, "maxqueue": allFields, "minconn": allFields,
		"weight": allFields, "slowstart": allFields, "backup": allFields,
		"error_limit": allFields, "observe": allFields, "on-error": allFields,
		"on-marked-down": allFields, "on-marked-up": allFields,
		"pool_low_conn": allFields, "pool_max_conn": allFields, "pool_purge_delay": allFields,
		"max_reuse": allFields,
		"ssl":       allFields, "verify": allFields, "ssl_min_ver": allFields, "ssl_max_ver": allFields,
		"alpn": allFields, "proto": allFields,
		"check-ssl": allFields, "check_alpn": allFields, "check_proto": allFields,
		"send-proxy": allFields, "send-proxy-v2": allFields, "check-send-proxy": allFields,
	},
}

// spaceSeparatedFields are kept keys whose HAProxy syntax is a space-separated list.
var spaceSeparatedFields = map[string]struct{}{"retry_on": {}}

// restrictBackendCR returns a copy of cr reduced to allowedBackendCRFields,
// with the sorted list of dropped keys. It fails when a kept value could carry
// more than its own setting, so the caller must then ignore the whole CR.
func restrictBackendCR(cr *models.Backend) (*models.Backend, []string, error) {
	raw, err := cr.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, nil, err
	}

	// HUG names the backend itself, so a name is never reported as dropped.
	delete(fields, "name")
	dropped := keepAllowedFields(fields, allowedBackendCRFields, "")
	slices.Sort(dropped)
	if err := checkFieldStrings("", fields); err != nil {
		return nil, nil, err
	}

	if raw, err = json.Marshal(fields); err != nil {
		return nil, nil, err
	}
	restricted := &models.Backend{}
	if err := restricted.UnmarshalBinary(raw); err != nil {
		return nil, nil, err
	}
	// Validate enforces the model enums and patterns; it requires a name.
	restricted.Name = "restricted"
	if err := restricted.Validate(strfmt.Default); err != nil {
		return nil, nil, err
	}
	restricted.Name = ""
	return restricted, dropped, nil
}

// keepAllowedFields deletes from fields every key absent from allowed and
// returns the deleted keys as dotted paths.
func keepAllowedFields(fields, allowed map[string]any, prefix string) []string {
	var dropped []string
	for key, value := range fields {
		sub, ok := allowed[key]
		if !ok {
			delete(fields, key)
			dropped = append(dropped, prefix+key)
			continue
		}
		subAllowed, isSubAllowed := sub.(map[string]any)
		subFields, isMap := value.(map[string]any)
		if isSubAllowed && subAllowed != nil && isMap {
			dropped = append(dropped, keepAllowedFields(subFields, subAllowed, prefix+key+".")...)
		}
	}
	return dropped
}

// checkFieldStrings rejects string values that could span several
// configuration words or lines. The models patterns do not cover every field.
func checkFieldStrings(key string, value any) error {
	switch v := value.(type) {
	case string:
		_, spaceSeparated := spaceSeparatedFields[key]
		if spaceSeparated {
			v = string(slices.DeleteFunc([]rune(v), func(r rune) bool { return r == ' ' }))
		}
		if maps.HasSpaceOrControl(v) {
			return fmt.Errorf("field %q holds whitespace or a control character", key)
		}
	case map[string]any:
		for k, sub := range v {
			if err := checkFieldStrings(k, sub); err != nil {
				return err
			}
		}
	case []any:
		for _, sub := range v {
			if err := checkFieldStrings(key, sub); err != nil {
				return err
			}
		}
	}
	return nil
}
