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

package handler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/events"
)

// retryReportingHandler counts the batches it receives and reports a configuration
// to retry until it has had the chance to apply it a second time, the way a failed
// apply leaves one behind.
type retryReportingHandler struct {
	mu        sync.Mutex
	batches   int
	batchSize []int
}

func (h *retryReportingHandler) HandleEventBatch(_ context.Context, batch events.EventBatch) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.batches++
	h.batchSize = append(h.batchSize, len(batch.Events))
}

func (h *retryReportingHandler) HasConfigurationToRetry() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	// One retry, then the configuration went through.
	return h.batches == 1
}

func (h *retryReportingHandler) batchCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.batches
}

func (h *retryReportingHandler) sizes() []int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]int(nil), h.batchSize...)
}

// A configuration that was not applied has no event of its own: the sync period is
// the only clock it has, so the loop must run a batch even when the batch carries
// nothing.
func TestEventLoopRunsABatchForAConfigurationWaitingForItsRetry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := &retryReportingHandler{}

	eventCh := make(chan any, 1)
	eventCh <- &events.UpsertEvent{Resource: &corev1.ConfigMap{}}

	period := 5 * time.Millisecond
	loop := NewEventLoop(
		EventLoopConfig{SyncPeriod: period, StartupSyncPeriod: period},
		eventCh,
		logger,
		handler,
		func(client.Object) schema.GroupVersionKind {
			return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		require.NoError(t, loop.Start(ctx))
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && handler.batchCount() < 2 {
		time.Sleep(time.Millisecond)
	}

	require.GreaterOrEqual(t, handler.batchCount(), 2,
		"the retry waited for an event that never comes")
	require.Equal(t, []int{1, 0}, handler.sizes()[:2],
		"the first batch carries the event, the retry carries none")

	// The retry is done: no further batch may run while no event arrives.
	settled := handler.batchCount()
	time.Sleep(20 * period)
	require.Equal(t, settled, handler.batchCount(),
		"a cycle that has nothing left to retry keeps the loop running")
}
