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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/events"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var isStartup = true

type EventLoop struct {
	handler    EventHandler
	logger     *slog.Logger
	eventCh    <-chan any
	extractGVK utilsk8s.ExtractGVK

	timer  *time.Timer
	timerC <-chan time.Time

	currentBatch events.EventBatch
	nextBatch    events.EventBatch

	loopCfg EventLoopConfig

	mu sync.Mutex

	handling bool
}

type EventLoopConfig struct {
	SyncPeriod        time.Duration
	StartupSyncPeriod time.Duration
}

// NewEventLoop creates a new EventLoop.
func NewEventLoop(
	loopCfg EventLoopConfig,
	eventCh <-chan any,
	logger *slog.Logger,
	handler EventHandler,
	extractGVK utilsk8s.ExtractGVK,
) *EventLoop {
	return &EventLoop{
		loopCfg:      loopCfg,
		eventCh:      eventCh,
		logger:       logger.With(logging.LogAttrCategory(logging.LogCategoryBatch)),
		handler:      handler,
		currentBatch: events.EventBatch{Events: make([]any, 0), BatchID: 0},
		nextBatch:    events.EventBatch{Events: make([]any, 0), BatchID: 1},
		extractGVK:   extractGVK,
	}
}

func (*EventLoop) NeedLeaderElection() bool {
	// Leader election (= be leader) is not required for this loop to start.
	// false = always run even if not leader
	return false
}

func (el *EventLoop) sleepTime() time.Duration {
	var sleepTime time.Duration
	if isStartup {
		isStartup = false
		switch el.loopCfg.StartupSyncPeriod {
		case 0:
			sleepTime = el.loopCfg.SyncPeriod
		default:
			sleepTime = el.loopCfg.StartupSyncPeriod
		}
	} else {
		sleepTime = el.loopCfg.SyncPeriod
	}

	return sleepTime
}

// Start starts the EventLoop.
// This method will block until the EventLoop stops, which will happen after the ctx is closed.
func (el *EventLoop) Start(ctx context.Context) error {
	// handlingDone is used to signal the completion of handling a batch.
	handlingDone := make(chan struct{})

	handleBatch := func() {
		el.SetHandling(true)
		go func(batch events.EventBatch) {
			el.handler.HandleEventBatch(ctx, batch)

			handlingDone <- struct{}{}
		}(el.currentBatch)
	}

	swapAndHandleBatch := func() {
		el.swapBatches()
		handleBatch()
	}

	// The event monitoring loop
	for {
		select {
		case <-ctx.Done():
			// Wait for the completion if a batch is being handled.
			el.stopTimer()
			if el.GetHandling() {
				<-handlingDone
			}
			return nil
		case e := <-el.eventCh:
			// Add the event to the current batch.
			el.nextBatch.Events = append(el.nextBatch.Events, e)

			el.logEvent(e)
			if el.nextBatch.BatchID == 1 {
				el.startTimer()
			}

		case <-el.timerC:
			el.timerC = nil
			el.timer = nil

			// If no batch is currently handled and events exist, handle batch now
			if !el.GetHandling() && len(el.nextBatch.Events) > 0 {
				swapAndHandleBatch()
			} else {
				el.startTimer()
			}
		case <-handlingDone:
			el.SetHandling(false)
			el.startTimer()
		}
	}
}

// swapBatches swaps the current and next batches.
func (el *EventLoop) swapBatches() {
	el.currentBatch, el.nextBatch = el.nextBatch, el.currentBatch
	el.nextBatch.Events = el.nextBatch.Events[:0]
	el.nextBatch.BatchID = el.currentBatch.BatchID + 1
}

func (el *EventLoop) SetHandling(value bool) {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.handling = value
}

func (el *EventLoop) GetHandling() bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.handling
}

func (el *EventLoop) startTimer() {
	if el.timer == nil {
		sleepTime := el.sleepTime()
		el.timer = time.NewTimer(sleepTime)
		el.timerC = el.timer.C
	}
}

func (el *EventLoop) stopTimer() {
	if el.timer != nil {
		if !el.timer.Stop() {
			select {
			case <-el.timer.C:
			default:
			}
		}
		el.timer = nil
		el.timerC = nil
	}
}

func (el *EventLoop) logEvent(e any) {
	var o client.Object
	eventType := ""
	switch obj := e.(type) {
	case *events.UpsertEvent:
		o = obj.Resource
		eventType = "UpsertEvent"
	case *events.DeleteEvent:
		o = obj.Type
		eventType = "DeleteEvent"
	}
	el.logger.LogAttrs(context.Background(), slog.LevelDebug,
		fmt.Sprintf("added an event to the batch %s", eventType),
		logging.LogAttrBatch(el.nextBatch.BatchID, len(el.nextBatch.Events)),
		logging.LogAttrResource(o, el.extractGVK(o)),
	)
}
