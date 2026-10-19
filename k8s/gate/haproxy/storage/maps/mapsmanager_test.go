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

package maps

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"
)

func newTestState() *MapFileState {
	handler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})
	logger := slog.New(handler)
	return NewMapFileState("fe", "test.map", logger)
}

func TestMapFileState_ApplyDesiredBackends_Full_WithPath(t *testing.T) {
	type testCase struct {
		name       string
		applyFn    func(m *MapFileState, ek EntryKey)
		expectVals map[EntryKey]string
	}

	tests := []testCase{
		// ----------------------------
		// 1 hostname, 1 backend
		{
			name: "1 hostname, 1 backend",
			applyFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "be1",
				{Hostname: "example.com", Path: "/api"}: "be1",
			},
		},
		// 2 hostnames, 1 backend
		{
			name: "2 hostnames, 1 backend, different origin",
			applyFn: func(m *MapFileState, ek EntryKey) {
				if ek.Hostname == "a.com" {
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				} else {
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
				}
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "be1",
				{Hostname: "b.com"}:             "be2",
				{Hostname: "a.com", Path: "/a"}: "be1",
				{Hostname: "b.com", Path: "/b"}: "be2",
			},
		},
		// 1 hostname, 2 backends different, different origin
		{
			name: "1 hostname, 2 backends different origin",
			applyFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
					map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               `{"a":"wr","l":"be1:1,be2:2"}`,
				{Hostname: "example.com", Path: "/api"}: `{"a":"wr","l":"be1:1,be2:2"}`,
			},
		},
		// 2 hostnames, 2 backends, different origin
		{
			name: "2 hostnames, 2 backends different origin",
			applyFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}, "be2": {ValueName: "be2", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be3": {ValueName: "be3", Weight: new(int32(3))}, "be4": {ValueName: "be4", Weight: new(int32(4))}})
				}
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             `{"a":"wr","l":"be1:1,be2:2"}`,
				{Hostname: "b.com"}:             `{"a":"wr","l":"be3:3,be4:4"}`,
				{Hostname: "a.com", Path: "/a"}: `{"a":"wr","l":"be1:1,be2:2"}`,
				{Hostname: "b.com", Path: "/b"}: `{"a":"wr","l":"be3:3,be4:4"}`,
			},
		},
		// 1 hostname, 2 identical backends, different origin, different weights
		{
			name: "1 hostname, 2 identical backends, different origin, different weights",
			applyFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(2))}})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(5))}})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "be1",
				{Hostname: "example.com", Path: "/api"}: "be1",
			},
		},
		// 2 hostnames, 2 identical backends, different origin, different weights
		{
			name: "2 hostnames, 2 identical backends, different origin, different weights",
			applyFn: func(m *MapFileState, ek EntryKey) {
				if ek.Hostname == "a.com" {
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(2))}})
				} else {
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(5))}})
				}
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "be1",
				{Hostname: "b.com"}:             "be1",
				{Hostname: "a.com", Path: "/a"}: "be1",
				{Hostname: "b.com", Path: "/b"}: "be1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, ek := range []EntryKey{
				{Hostname: "example.com"},               // TLSRoute / no path
				{Hostname: "example.com", Path: "/api"}, // HTTPRoute / with path
				{Hostname: "a.com"},                     // TLSRoute
				{Hostname: "a.com", Path: "/a"},         // HTTPRoute
				{Hostname: "b.com"},                     // TLSRoute
				{Hostname: "b.com", Path: "/b"},         // HTTPRoute
			} {
				state := newTestState()
				tt.applyFn(state, ek)
				state.ProcessMapFiles()
				expected, ok := tt.expectVals[ek]
				if !ok {
					continue
				}
				entry := state.Entries[ek]
				if entry == nil {
					t.Fatalf("entry %v not found", ek)
				}
				got := BuildRouteValue(entry.DesiredValue)
				if got != expected {
					t.Fatalf("for key %v\nexpected: %s\ngot:      %s", ek, expected, got)
				}
			}
		})
	}
}

func TestMapFileState_ApplyDesiredBackends_DeleteScenarios(t *testing.T) {
	type testCase struct {
		name       string
		setupFn    func(m *MapFileState, ek EntryKey)
		deleteFn   func(m *MapFileState, ek EntryKey)
		expectVals map[EntryKey]string
	}

	tests := []testCase{
		// ----------------------------
		// 1 hostname, 1 backend
		{
			name: "1 hostname, 1 backend deleted",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				// supprime le backend
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// 2 hostnames, 1 backend, different origin
		{
			name: "2 hostnames, 1 backend deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
		// 1 hostname, 2 backends, different origin
		{
			name: "1 hostname, 2 backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
					map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// 2 hostnames, 2 backends different origin
		{
			name: "2 hostnames, 2 backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}, "be2": {ValueName: "be2", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be3": {ValueName: "be3", Weight: new(int32(3))}, "be4": {ValueName: "be4", Weight: new(int32(4))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
		// 1 hostname, 2 identical backends, different origin
		{
			name: "1 hostname, 2 identical backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(2))}})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(5))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// 2 hostnames, 2 identical backends, different origin
		{
			name: "2 hostnames, 2 identical backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(5))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
	}

	entries := []EntryKey{
		{Hostname: "example.com"},
		{Hostname: "example.com", Path: "/api"},
		{Hostname: "a.com"},
		{Hostname: "a.com", Path: "/a"},
		{Hostname: "b.com"},
		{Hostname: "b.com", Path: "/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, ek := range entries {
				state := newTestState()
				tt.setupFn(state, ek)  // crée les backends
				tt.deleteFn(state, ek) // supprime certains backends
				state.ProcessMapFiles()

				expected, ok := tt.expectVals[ek]
				if !ok {
					continue
				}
				entry := state.Entries[ek]
				got := ""
				if entry != nil {
					got = BuildRouteValue(entry.DesiredValue)
				}
				if got != expected {
					t.Fatalf("for key %v\nexpected: %s\ngot:      %s", ek, expected, got)
				}
			}
		})
	}
}

func TestMapFileState_DeleteBackends(t *testing.T) {
	type testCase struct {
		name       string
		setupFn    func(m *MapFileState, ek EntryKey)
		deleteFn   func(m *MapFileState, ek EntryKey)
		expectVals map[EntryKey]string
	}

	tests := []testCase{
		// 1 hostname, 1 backend
		{
			name: "1 hostname, 1 backend deleted",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// 2 hostnames, 1 backend, different origin
		{
			name: "2 hostnames, 1 backend deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
		// 1 hostname, 2 backends different origin
		{
			name: "1 hostname, 2 backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
					map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// 2 hostnames, 2 backends different origin
		{
			name: "2 hostnames, 2 backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}, "be2": {ValueName: "be2", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be3": {ValueName: "be3", Weight: new(int32(3))}, "be4": {ValueName: "be4", Weight: new(int32(4))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
		// 1 hostname, 2 identical backends, different origin
		{
			name: "1 hostname, 2 identical backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(2))}})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(5))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// 2 hostnames, 2 identical backends, different origin
		{
			name: "2 hostnames, 2 identical backends deleted, different origin",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(5))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
	}

	entries := []EntryKey{
		{Hostname: "example.com"},
		{Hostname: "example.com", Path: "/api"},
		{Hostname: "a.com"},
		{Hostname: "a.com", Path: "/a"},
		{Hostname: "b.com"},
		{Hostname: "b.com", Path: "/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, ek := range entries {
				state := newTestState()
				tt.setupFn(state, ek)  // crée les backends
				tt.deleteFn(state, ek) // supprime certains backends
				state.ProcessMapFiles()

				expected, ok := tt.expectVals[ek]
				if !ok {
					continue
				}
				entry := state.Entries[ek]
				got := ""
				if entry != nil {
					got = BuildRouteValue(entry.DesiredValue)
				}
				if got != expected {
					t.Fatalf("for key %v\nexpected: %s\ngot:      %s", ek, expected, got)
				}
			}
		})
	}
}

func TestMapFileState_BackendDeletions(t *testing.T) {
	type testCase struct {
		name       string
		setupFn    func(m *MapFileState, ek EntryKey)
		deleteFn   func(m *MapFileState, ek EntryKey)
		expectVals map[EntryKey]string
	}

	tests := []testCase{
		// ------------------------------------------------
		// 1 hostname, 1 backend → suppression totale
		{
			name: "1 hostname, 1 backend deleted",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "",
				{Hostname: "example.com", Path: "/api"}: "",
			},
		},
		// ------------------------------------------------
		// 1 hostname, 2 backends → suppression partielle
		{
			name: "1 hostname, 2 backends, delete one only",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{
						"be1": {ValueName: "be1", Weight: new(int32(1))},
						"be2": {ValueName: "be2", Weight: new(int32(2))},
					})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "be2", // un backend restant → format simple
				{Hostname: "example.com", Path: "/api"}: "be2",
			},
		},
		// ------------------------------------------------
		// 1 hostname, 2 backends, different weights, partial deletion
		{
			name: "1 hostname, 2 identical backends different weights, delete one",
			setupFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{
						"be1": {ValueName: "be1", Weight: new(int32(2))},
						"be2": {ValueName: "be2", Weight: new(int32(5))},
					})
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
					map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(5))}})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "example.com"}:               "be2",
				{Hostname: "example.com", Path: "/api"}: "be2",
			},
		},
		// ------------------------------------------------
		// 2 hostnames, 1 backend → suppression totale, différentes ressources
		{
			name: "2 hostnames, 1 backend deleted, different origins",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"}, map[string]*WeightedValue{})
				m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"}, map[string]*WeightedValue{})
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "",
				{Hostname: "b.com"}:             "",
				{Hostname: "a.com", Path: "/a"}: "",
				{Hostname: "b.com", Path: "/b"}: "",
			},
		},
		// ------------------------------------------------
		// 2 hostnames, 2 backends → suppression partielle
		{
			name: "2 hostnames, 2 backends, delete one each",
			setupFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be1": {ValueName: "be1", Weight: new(int32(1))}, "be2": {ValueName: "be2", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be3": {ValueName: "be3", Weight: new(int32(3))}, "be4": {ValueName: "be4", Weight: new(int32(4))}})
				}
			},
			deleteFn: func(m *MapFileState, ek EntryKey) {
				switch ek.Hostname {
				case "a.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res1"},
						map[string]*WeightedValue{"be2": {ValueName: "be2", Weight: new(int32(2))}})
				case "b.com":
					m.ApplyDesiredBackends(ek, ResourceOrigin{"default", "res2"},
						map[string]*WeightedValue{"be3": {ValueName: "be3", Weight: new(int32(3))}})
				}
			},
			expectVals: map[EntryKey]string{
				{Hostname: "a.com"}:             "be2",
				{Hostname: "b.com"}:             "be3",
				{Hostname: "a.com", Path: "/a"}: "be2",
				{Hostname: "b.com", Path: "/b"}: "be3",
			},
		},
	}

	entries := []EntryKey{
		{Hostname: "example.com"},
		{Hostname: "example.com", Path: "/api"},
		{Hostname: "a.com"},
		{Hostname: "a.com", Path: "/a"},
		{Hostname: "b.com"},
		{Hostname: "b.com", Path: "/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, ek := range entries {
				state := newTestState()
				tt.setupFn(state, ek)
				state.ProcessMapFiles()
				state.Reset()
				tt.deleteFn(state, ek)
				state.ProcessMapFiles()

				expected, ok := tt.expectVals[ek]
				if !ok {
					continue
				}
				entry := state.Entries[ek]
				got := ""
				if entry != nil {
					got = BuildRouteValue(entry.DesiredValue)
				}
				if got != expected {
					t.Fatalf("for key %v\nexpected: %s\ngot:      %s", ek, expected, got)
				}
			}
		})
	}
}

// The purge a route owes its map entries, asked of the file rather than reconstructed
// from the listeners the route had. Those entries can outlive any record of them: a route
// created then deleted never had its attachments filled, and a reconstruction from its
// spec cannot see the entry a ListenerSet contributed.
func TestMapFileState_RemoveEntriesFromRoute(t *testing.T) {
	route := types.NamespacedName{Namespace: "tenant", Name: "route"}
	otherRoute := types.NamespacedName{Namespace: "tenant", Name: "route-other"}
	otherNamespace := types.NamespacedName{Namespace: "elsewhere", Name: "route"}

	originOf := func(listener string, key types.NamespacedName) ResourceOrigin {
		return NewRouteOrigin("HTTPRoute", listener, key)
	}
	// ApplyRoute is the entry point the production code uses, and the one that keeps
	// EntriesByResource: ApplyDesiredBackends alone does not, so a test built on it
	// would be testing a purge over a bookkeeping nothing had filled.
	applied := func(m *MapFileState, host, name string, origin ResourceOrigin) {
		m.ApplyRoute(origin, map[EntryKey]map[string]*WeightedValue{
			{Hostname: host}: {name: {ValueName: name, Weight: new(int32(1))}},
		})
	}

	t.Run("drops every listener the route wrote, across frontends", func(t *testing.T) {
		m := newTestState()
		// Two listeners, so two origins: a route's entries are not confined to one
		// frontend, and the purge may not assume they are.
		applied(m, "a.com", "be-a", originOf("gw_listener-a", route))
		applied(m, "b.com", "be-b", originOf("gw_listener-b", route))
		applied(m, "kept.com", "be-c", originOf("gw_listener-a", otherRoute))

		removed := m.RemoveEntriesFromRoute("HTTPRoute", route)

		assert.Equal(t, 2, removed)
		assert.Empty(t, m.EntriesByResource[originOf("gw_listener-a", route)])
		assert.Empty(t, m.EntriesByResource[originOf("gw_listener-b", route)])
		assert.NotEmpty(t, m.EntriesByResource[originOf("gw_listener-a", otherRoute)],
			"another route's entries are not ours to drop")
	})

	t.Run("another namespace's entries of the same name are left alone", func(t *testing.T) {
		m := newTestState()
		applied(m, "a.com", "be-a", originOf("gw_listener", route))
		applied(m, "b.com", "be-b", originOf("gw_listener", otherNamespace))

		removed := m.RemoveEntriesFromRoute("HTTPRoute", route)

		assert.Equal(t, 1, removed)
		assert.Empty(t, m.EntriesByResource[originOf("gw_listener", route)])
		assert.NotEmpty(t, m.EntriesByResource[originOf("gw_listener", otherNamespace)])
	})

	t.Run("a route whose name merely ends the same way is left alone", func(t *testing.T) {
		m := newTestState()
		applied(m, "a.com", "be-a", originOf("gw_listener", otherRoute))

		removed := m.RemoveEntriesFromRoute("HTTPRoute", route)

		assert.Equal(t, 0, removed, "the name is a path segment, not a suffix match")
		assert.NotEmpty(t, m.EntriesByResource[originOf("gw_listener", otherRoute)])
	})

	t.Run("another kind and a listener origin are left alone", func(t *testing.T) {
		m := newTestState()
		httpOrigin := originOf("gw_listener", route)
		tlsOrigin := NewRouteOrigin("TLSRoute", "gw_listener", route)
		listenerOrigin := ResourceOrigin{Namespace: route.Namespace, Name: "gw_listener/" + route.Name}
		applied(m, "http.com", "be-http", httpOrigin)
		applied(m, "tls.com", "be-tls", tlsOrigin)
		applied(m, "listener.com", "listener", listenerOrigin)
		m.ProcessMapFiles()
		m.Reset()

		assert.Equal(t, 1, m.RemoveEntriesFromRoute("HTTPRoute", route))
		m.ProcessMapFiles()
		assert.Empty(t, m.Entries[EntryKey{Hostname: "http.com"}].DesiredValue)
		assert.Contains(t, m.Entries[EntryKey{Hostname: "tls.com"}].DesiredValue, "be-tls")
		assert.Contains(t, m.Entries[EntryKey{Hostname: "listener.com"}].DesiredValue, "listener")
	})
}
