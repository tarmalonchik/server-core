package core

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

type defaultLogger struct{}

func (l *defaultLogger) Log(ctx context.Context, message string) {
	logrus.WithContext(ctx).WithError(fmt.Errorf("server-core")).Errorf(message)
}
