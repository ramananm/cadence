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
	"github.com/stretchr/testify/require"
	"github.com/uber-common/bark"
	workflow "github.com/uber/cadence/.gen/go/shared"
	"github.com/uber/cadence/common"
	"github.com/uber/cadence/common/cache"
	"github.com/uber/cadence/common/persistence"
	"github.com/uber/cadence/common/service/dynamicconfig"
	"sync/atomic"
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

func TestNewRateLimiter(t *testing.T) {
	maxDispatch := float64(0.01)
	rl := newRateLimiter(&maxDispatch, time.Second, _minBurst)
	limiter := rl.globalLimiter.Load().(*rate.Limiter)
	assert.Equal(t, _minBurst, limiter.Burst())
}

func createTestTaskListManager() *taskListManagerImpl {
	return createTestTaskListManagerWithConfig(defaultTestConfig())
}

func createTestTaskListManagerWithConfig(cfg *Config) *taskListManagerImpl {
	logger := bark.NewLoggerFromLogrus(log.New())
	tm := newTestTaskManager(logger)
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

func TestIsTaskAddedRecently(t *testing.T) {
	tlm := createTestTaskListManager()

	tlm.lastAddTaskTime = time.Now()
	require.True(t, tlm.isTaskAddedRecently())

	tlm.lastAddTaskTime = time.Now().Add(-maxAddTasksIdleTime)
	require.False(t, tlm.isTaskAddedRecently())

	tlm.lastAddTaskTime = time.Now().Add(1 * time.Second)
	require.True(t, tlm.isTaskAddedRecently())
}

func TestCheckIdleTaskList(t *testing.T) {
	cfg := NewConfig(dynamicconfig.NewNopCollection())
	cfg.IdleTasklistCheckInterval = dynamicconfig.GetDurationPropertyFnFilteredByTaskListInfo(10 * time.Millisecond)

	// Idle
	tlm := createTestTaskListManagerWithConfig(cfg)
	tlm.Start()
	time.Sleep(20 * time.Millisecond)
	require.False(t, atomic.CompareAndSwapInt32(&tlm.stopped, 0, 1))

	// Active poll-er
	tlm = createTestTaskListManagerWithConfig(cfg)
	tlm.updatePollerInfo(pollerIdentity{identity: "test-poll"})
	require.Equal(t, 1, len(tlm.GetAllPollerInfo()))
	require.True(t, tlm.lastAddTaskTime.IsZero())
	tlm.Start()
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(0), tlm.stopped)
	tlm.Stop()
	require.Equal(t, int32(1), tlm.stopped)

	// Active adding task
	tlm = createTestTaskListManagerWithConfig(cfg)
	require.Equal(t, 0, len(tlm.GetAllPollerInfo()))
	require.True(t, tlm.lastAddTaskTime.IsZero())
	tlm.Start()
	wid := "wid"
	rid := "0d00698f-08e1-4d36-a3e2-3bf109f5d2d6"
	tlm.AddTask(
		&workflow.WorkflowExecution{
			WorkflowId: common.StringPtr(wid),
			RunId:      common.StringPtr(rid),
		},
		&persistence.TaskInfo{
			DomainID:   "domainID",
			WorkflowID: "wid",
			RunID:      rid,
			TaskID:     1,
			ScheduleID: 1,
		},
	)
	require.False(t, tlm.lastAddTaskTime.IsZero())
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(0), tlm.stopped)
	tlm.Stop()
	require.Equal(t, int32(1), tlm.stopped)
}
