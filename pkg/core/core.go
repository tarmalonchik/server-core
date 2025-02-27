package core

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	runnableStack chan *runner       // stack with jobs need to run
	errorChan     chan error         // stack with errors
	logger        WriteLog           // you can use this logger for custom logging
	cancel        context.CancelFunc // context.Cancel func
	timeout       time.Duration      // time when forced termination will happen after crushing
	jobsDone      chan interface{}   // the channel to signal when all work done
}

// NewCore logger is optional field, can be nil
func NewCore(logger WriteLog, timeout time.Duration, parallelCount uint8) Core {
	if logger == nil {
		logger = &defaultLogger{}
	}
	return &core{
		runnableStack: make(chan *runner, parallelCount),
		errorChan:     make(chan error, parallelCount),
		jobsDone:      make(chan interface{}),
		logger:        logger,
		timeout:       timeout,
	}
}

func (c *core) AddRunner(in RunnerFunc, opts ...RunnerOpt) {
	c.runnableStack <- newRunner(in, opts...)
}

func (c *core) Launch(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go c.waitForInterruption()
	go c.logErrors(ctx)
	go c.runRunners(ctx)
	<-ctx.Done()
	c.waitGraceful()
}

func (c *core) runRunners(ctx context.Context) {
	for item := range c.runnableStack {
		if ctx.Err() != nil {
			close(c.runnableStack)
			continue
		}
		go func(*runner) {
			err, wasPanic := item.run(ctx)
			if item.wireLog && err != nil {
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
	fmt.Println("here")
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
