package core

import (
	"context"
	"fmt"
	"runtime/debug"
)

var (
	panicError = fmt.Errorf("panic happened")
)

type RunnerOpt func(v *runner)
type RunnerFunc func(ctx context.Context) error

func newRunner(runnerFunc RunnerFunc, options ...RunnerOpt) *runner {
	r := runner{
		runnerFunc: runnerFunc,
	}
	for _, opt := range options {
		opt(&r)
	}
	return &r
}

func RepeatOnError() RunnerOpt {
	return func(v *runner) {
		v.repeatOnError = true
	}
}

func RepeatOnPanic() RunnerOpt {
	return func(v *runner) {
		v.repeatOnPanic = true
	}
}

func RepeatOnFinish() RunnerOpt {
	return func(v *runner) {
		v.repeatOnFinish = true
	}
}

func WriteLogEnable() RunnerOpt {
	return func(v *runner) {
		v.writeLog = true
	}
}

type runner struct {
	runnerFunc     func(ctx context.Context) error
	repeatOnError  bool
	repeatOnPanic  bool
	repeatOnFinish bool
	writeLog       bool
}

func (r *runner) run(ctx context.Context) (err error, wasPanic bool) {
	ctx, cancel := context.WithCancel(ctx)

	defer func() {
		cancel()
		if recoverErr := recover(); recoverErr != nil {
			err = fmt.Errorf("%w: %v\n%s", panicError, panicToString(recoverErr), string(debug.Stack()))
			wasPanic = true
		}
		<-ctx.Done()
	}()

	err = r.runnerFunc(ctx)
	return
}

func panicToString(panicErr interface{}) string {
	if panicErr == nil {
		return ""
	}
	switch e := panicErr.(type) {
	case string:
		return e
	case []byte:
		return string(e)
	}
	return fmt.Sprintf("%+v", panicErr)
}
