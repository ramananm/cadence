// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package matching

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"

	"github.com/uber/cadence/common/mocks"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/uber-common/bark"
	workflow "github.com/uber/cadence/.gen/go/shared"
	"github.com/uber/cadence/common"
	"github.com/uber/cadence/common/cache"
	"github.com/uber/cadence/common/persistence"
)

const _minBurst = 10000

func TestDeliverBufferTasks(t *testing.T) {
	tests := []func(tlm *taskListManagerImpl){
		func(tlm *taskListManagerImpl) { close(tlm.taskBuffer) },
		func(tlm *taskListManagerImpl) { close(tlm.deliverBufferShutdownCh) },
		func(tlm *taskListManagerImpl) { tlm.cancelFunc() },
	}
	for _, test := range tests {
		tlm := createTestTaskListManager()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			tlm.deliverBufferTasksForPoll()
		}()
		test(tlm)
		// deliverBufferTasksForPoll should stop after invocation of the test function
		wg.Wait()
	}
}

func TestDeliverBufferTasks_NoPollers(t *testing.T) {
	tlm := createTestTaskListManager()
	tlm.taskBuffer <- &persistence.TaskInfo{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		tlm.deliverBufferTasksForPoll()
		wg.Done()
	}()
	time.Sleep(100 * time.Millisecond) // let go routine run first and block on tasksForPoll
	tlm.drainTaskList()
	tlm.cancelFunc()
	wg.Wait()
}

func TestNewRateLimiter(t *testing.T) {
	maxDispatch := float64(0.01)
	rl := newRateLimiter(&maxDispatch, time.Second, _minBurst)
	limiter := rl.globalLimiter.Load().(*rate.Limiter)
	assert.Equal(t, _minBurst, limiter.Burst())
}

func createTestTaskListManager() *taskListManagerImpl {
	logger := bark.NewLoggerFromLogrus(log.New())
	tm := newTestTaskManager(logger)
	cfg := defaultTestConfig()
	mockDomainCache := &cache.DomainCacheMock{}
	mockDomainCache.On("GetDomainByID", mock.Anything).Return(cache.CreateDomainCacheEntry("domainName"), nil)
	me := newMatchingEngine(
		cfg, tm, &mocks.HistoryClient{}, logger, mockDomainCache,
	)
	tl := "tl"
	dID := "domain"
	tlID := &taskListID{domainID: dID, taskListName: tl, taskType: persistence.TaskListTypeActivity}
	tlKind := common.TaskListKindPtr(workflow.TaskListKindNormal)
	tlMgr, err := newTaskListManager(me, tlID, tlKind, cfg)
	if err != nil {
		logger.Fatalf("error when createTestTaskListManager: %v", err)
	}
	return tlMgr.(*taskListManagerImpl)
}
