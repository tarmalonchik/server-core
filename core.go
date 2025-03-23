package core

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

type WriteLog interface {
	Log(ctx context.Context, message string)
}

type Core interface {
	Launch(ctx context.Context)
	AddRunner(in RunnerFunc, opts ...RunnerOpt)
}

type core struct {
	runnableCount  int64
	finishersCount int64

	runnableStack  chan *runner // stack with jobs need to run
	finishersStack chan *runner // stack with jobs run when finish

	errorChan chan error         // stack with errors
	logger    WriteLog           // you can use this logger for custom logging
	cancel    context.CancelFunc // context.Cancel func
	timeout   time.Duration      // time when forced termination will happen after crushing
	jobsDone  chan interface{}   // the channel to signal when all work done
}

// NewCore logger is optional field, can be nil
func NewCore(logger WriteLog, timeout time.Duration, parallelCount uint8) Core {
	if logger == nil {
		logger = &defaultLogger{}
	}
	return &core{
		runnableStack:  make(chan *runner, parallelCount),
		finishersStack: make(chan *runner, parallelCount),
		errorChan:      make(chan error, parallelCount),
		jobsDone:       make(chan interface{}),
		logger:         logger,
		timeout:        timeout,
	}
}

func (c *core) AddRunner(in RunnerFunc, opts ...RunnerOpt) {
	r := &runner{}
	for i := range opts {
		opts[i](r)
	}
	if r.isFinisher && (r.repeatOnFinish || r.repeatOnPanic || r.repeatOnError) {
		panic("finisher should have no repeater")
	}

	if r.isFinisher {
		c.finishersStack <- newRunner(in, opts...)
		c.incFinishers()
	} else {
		c.runnableStack <- newRunner(in, opts...)
		c.inc()
	}
}

func (c *core) Launch(ctx context.Context) {
	originalContext := ctx
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go c.waitForInterruption()
	go c.logErrors(originalContext)
	go c.runRunners(originalContext, ctx)
	<-ctx.Done()
	c.waitGraceful()
}

func (c *core) runRunners(originalContext, ctx context.Context) {
	for item := range c.runnableStack {
		if ctx.Err() != nil {
			close(c.runnableStack)
			continue
		}
		go func(*runner) {
			err, wasPanic := item.run(ctx)
			if item.writeLog && err != nil {
				c.errorChan <- err
			}

			if wasPanic && item.repeatOnPanic {
				time.Sleep(100 * time.Millisecond)
				c.runnableStack <- item
				return
			}

			if err != nil && !wasPanic && item.repeatOnError {
				time.Sleep(100 * time.Millisecond)
				c.runnableStack <- item
				return
			}

			if item.repeatOnFinish {
				time.Sleep(100 * time.Millisecond)
				c.runnableStack <- item
				return
			}

			if c.dec() <= 0 {
				close(c.runnableStack)
			}
		}(item)
	}

	for item := range c.finishersStack {
		if originalContext.Err() != nil {
			close(c.finishersStack)
			continue
		}

		go func(*runner) {
			err, _ := item.run(originalContext)
			if item.writeLog && err != nil {
				c.errorChan <- err
			}

			if c.decFinishers() <= 0 {
				close(c.finishersStack)
			}
		}(item)
	}

	close(c.errorChan)
	c.cancel()
}

func (c *core) logErrors(ctx context.Context) {
	for err := range c.errorChan {
		c.logErr(ctx, err)
	}
	c.jobsDone <- nil
}

func (c *core) logErr(ctx context.Context, err error) {
	if c.logger == nil {
		if strings.Contains(err.Error(), panicError.Error()) {
			logrus.WithError(err).Errorf("panic happened")
		} else {
			logrus.WithError(err).Errorf("error happened")
		}
		return
	}
	if strings.Contains(err.Error(), panicError.Error()) {
		c.logger.Log(ctx, fmt.Sprintf("panic happened: %s", err.Error()))
	} else {
		c.logger.Log(ctx, fmt.Sprintf("error happened: %s", err.Error()))
	}
}

func (c *core) waitGraceful() {
	timeout, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	select {
	case <-timeout.Done():
		fmt.Println("graceful timeout done")
		return
	case <-c.jobsDone:
		fmt.Println("jobs done")
		return
	}
}

func (c *core) waitForInterruption() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	c.cancel()
}

func (c *core) inc() {
	atomic.AddInt64(&c.runnableCount, 1)
}

func (c *core) dec() int64 {
	return atomic.AddInt64(&c.runnableCount, -1)
}

func (c *core) incFinishers() {
	atomic.AddInt64(&c.finishersCount, 1)
}
func (c *core) decFinishers() int64 {
	return atomic.AddInt64(&c.finishersCount, -1)
}
